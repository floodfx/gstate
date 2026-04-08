# gstate

A type-safe Statechart library for Go, inspired by [XState](https://xstate.js.org/).

`gstate` allows you to model complex application logic using finite state machines and statecharts. Unlike traditional logic scattered across `if/else` blocks and boolean flags, statecharts provide a formal, visual, and structured way to define how your system behaves.

## What is a Statechart?

A Statechart is an extension of a Finite State Machine (FSM). While a basic FSM has a set of states and transitions, a Statechart adds:
- **Hierarchy**: States can contain other states (Nested States).
- **Orthogonality**: Multiple states can be active at once (Parallel States).
- **Broadcast**: Events can trigger transitions in multiple regions.
- **History**: The ability to "remember" where you were before leaving a state.

## Installation

```bash
go get github.com/floodfx/gstate
```

---

## 1. The Basics: States, Events, and Transitions

Every statechart starts with three core concepts:
- **State**: A specific condition or "mode" of your system (e.g., `Idle`, `Loading`, `Success`).
- **Event**: Something that happens (e.g., `START`, `MOUSE_CLICK`, `TIMEOUT`).
- **Transition**: A rule that says: "When in state A, if event E happens, move to state B."

### Example: A Simple Toggle with Typed Constants
```go
type MyState string
type MyEvent string

const (
    StateOff MyState = "off"
    StateOn  MyState = "on"
)

const (
    EventToggle MyEvent = "TOGGLE"
)

machine := gstate.New[MyState, MyEvent, any]("toggle").
    Initial(StateOff).
    State(StateOff, func(s *gstate.StateBuilder[MyState, MyEvent, any]) {
        s.On(EventToggle).GoTo(StateOn)
    }).
    State(StateOn, func(s *gstate.StateBuilder[MyState, MyEvent, any]) {
        s.On(EventToggle).GoTo(StateOff)
    }).
    Build()
```

---

## 2. Type Safety & Generics

One of the core strengths of `gstate` is its use of Go 1.18+ generics to provide strict type safety.

The library uses three generic parameters: `[S ~string, E ~string, C any]`.

- **`S` (State ID)**: By using a custom string type (e.g., `type MyState string`), you ensure that `Initial()`, `State()`, and `GoTo()` only accept valid state identifiers.
- **`E` (Event ID)**: Similarly, `On(event)` only accepts events of your specific type.
- **`C` (Context)**: The data your machine holds is strictly typed. Actions and guards receive this exact type, eliminating the need for `interface{}` casting.

**Benefits:**
- **No Typos**: Compilers will catch `actor.Send("TYPO")` if your event type is strictly defined.
- **IDE Support**: Autocomplete works for states, events, and context fields.
- **Safety**: Guards and Actions are verified at compile time to work with your specific data structure.

---

## 3. Managing Data with Context (`Assign`)

Statecharts aren't just about labels; they often need to hold data. In `gstate`, this is called **Context**.

Transitions can perform **Actions** to update this data. In Go, these are pure functions: `func(C) C`.

```go
type CounterCtx struct {
    Count int
}

s.On("INCREMENT").
    Assign(func(c CounterCtx) CounterCtx {
        c.Count++
        return c
    })
```

#### Safe Snapshots with `Cloner`

If your Context contains reference types (pointers, slices, maps), `Snapshot()` and `Context()` might suffer from race conditions or shared state. You can implement the `Cloner` interface to provide a deep copy:

```go
type MyCtx struct {
    Data []int
}

func (c MyCtx) Clone() MyCtx {
    newData := make([]int, len(c.Data))
    copy(newData, c.Data)
    return MyCtx{Data: newData}
}
```

---

## 4. Entry and Exit Actions

States can define actions that run whenever they are entered or exited. This is useful for setup/teardown, logging, or any side effect tied to a state's lifecycle.

```go
s.State(StateActive, func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Entry(func(c MyContext) MyContext {
        fmt.Println("[active] Entering state...")
        return c
    })

    s.Exit(func(c MyContext) MyContext {
        fmt.Println("[active] Leaving state...")
        return c
    })

    s.On(EventStop).GoTo(StateIdle)
})
```

- **`Entry`** runs when the state is entered, before any child states are resolved.
- **`Exit`** runs when the state is left, as part of the transition.

---

## 5. Hierarchical (Nested) States

In a complex system, some states are "sub-modes" of others. For example, a `User` state might have `Guest` and `LoggedIn` sub-states.

**Why use this?**
- **Bubbling**: If a child state doesn't handle an event, it "bubbles up" to the parent.
- **Organization**: Group related logic together.
- **Common Actions**: Define an `Entry` action on a parent that runs regardless of which child is entered.

```go
s.State("parent", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Initial("childA")
    
    // If ANY child receives "RESET", we go to "parent.childA"
    s.On("RESET").GoTo("childA")

    s.State("childA", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) { ... })
    s.State("childB", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) { ... })
})
```

---

## 6. History States

History allows a compound state to remember which of its children was active before it was exited. When you re-enter the state, it resumes where it left off instead of going to the `Initial` child.

Two history types are available:
- **`gstate.Shallow`**: Remembers the direct child that was active.
- **`gstate.Deep`**: Remembers all active descendants in the hierarchy.

```go
machine := gstate.New[MyState, MyEvent, MyContext]("history_demo").
    Initial("app").
    State("app", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
        s.History(gstate.Shallow)
        s.Initial("screen1")

        s.State("screen1", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("SWITCH").GoTo("screen2")
        })
        s.State("screen2", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("SWITCH").GoTo("screen1")
        })

        s.On("GO_IDLE").GoTo("idle")
    }).
    State("idle", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
        s.On("WAKE").GoTo("app")
    }).
    Build()
```

In this example, if the user navigates to `screen2` and then goes idle, `WAKE` will return them to `screen2` (not the initial `screen1`).

---

## 7. Parallel States

Sometimes a system is in multiple modes at once. A text editor might be `Focused` while also having `Bold` enabled.

Parallel states allow you to define regions that operate independently. Use `actor.States()` to see all active states.

```go
s.State("active", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Type(gstate.Parallel)

    s.State("keyboard", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
        s.Initial("caps_off")
        s.State("caps_off", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("CAPS_LOCK").GoTo("caps_on")
        })
        s.State("caps_on", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("CAPS_LOCK").GoTo("caps_off")
        })
    })

    s.State("mouse", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
        s.Initial("not_clicked")
        s.State("not_clicked", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("CLICK").GoTo("clicked")
        })
        s.State("clicked", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
            s.On("RELEASE").GoTo("not_clicked")
        })
    })
})

// ...
fmt.Printf("Active States: %v\n", actor.States())
// Output: Active States: [active keyboard caps_off mouse not_clicked]
```

---

## 8. Side Effects: Invoke and After

### Invoked Services (`Invoke`)
Used for asynchronous work (like an API call). The service starts when you enter the state and is **automatically cancelled** (via `context.Context`) if you leave the state before it finishes.

```go
s.Invoke(func(ctx context.Context, c MyCtx) error {
    // This goroutine is managed by the Actor
    return doExpensiveWork(ctx)
}, "onSuccessState", "onErrorState")
```

### Delayed Transitions (`After`)
Transitions that happen automatically after a duration.

```go
s.State("loading", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    // If we are stuck here for 5 seconds, move to "error"
    s.After(5 * time.Second).GoTo("error")
})
```

---

## 9. Transient Logic (`Always`)

`Always` transitions fire immediately if their **Guard** (a condition function) is met. They don't wait for an external event. This is useful for "decider" states.

```go
s.State("check_balance", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Always().
        Guard(func(c MyCtx) bool { return c.Balance > 100 }).
        GoTo("premium_user")
    
    s.Always().GoTo("regular_user") // Fallback
})
```

---

## 10. Final States

A `Final` state indicates the completion of its parent's process. Once entered, no further transitions are processed from that state.

```go
s.State("done", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Type(gstate.Final)
})
```

---

## The Actor: Running a Machine

A `Machine` is a static blueprint. To actually run it, you create an **Actor**. The Actor holds the live state, processes events, and manages async services.

### Creating an Actor

```go
// Start with default options (mailbox size of 100)
actor := gstate.Start(machine, MyContext{Count: 0})

// Or start with custom options
actor := gstate.StartWithOptions(machine, MyContext{Count: 0}, gstate.Options{
    MailboxSize: 500,
})
```

### Sending Events

```go
actor.Send(EventIncrement)
actor.Send(EventStart)
```

Events are queued in a channel-based mailbox and processed sequentially, ensuring state transitions are never concurrent.

### Reading State

```go
// Get the deepest active leaf state
state := actor.State()

// Get ALL active states (useful for parallel states)
states := actor.States()

// Get a thread-safe copy of the context data
ctx := actor.Context()

// Get a full snapshot (active states, history, and context)
snap := actor.Snapshot()
```

All read methods are thread-safe (protected by `RWMutex`).

### Stopping an Actor

```go
actor.Stop()
```

`Stop()` cancels all running invocations, stops all timers, and closes the mailbox. It is safe to call multiple times.

---

## Persistence: Snapshot and Hydrate

Snapshots allow you to serialize the full state of an Actor and restore it later. This is critical for long-running workflows that must survive process restarts.

```go
// 1. Capture the current state
snapshot := actor.Snapshot()

// 2. Serialize to JSON (for storage in a database, file, etc.)
data, _ := json.MarshalIndent(snapshot, "", "  ")

// 3. Later, deserialize and restore
var loaded gstate.Snapshot[MyState, MyContext]
json.Unmarshal(data, &loaded)

actor2 := gstate.Hydrate(machine, loaded)
// actor2 is now in exactly the same state as the original
```

A `Snapshot` contains:
- **`Active []S`** — all currently active states
- **`History map[S]S`** — the history map (parent → remembered child)
- **`Context C`** — the context data

`Hydrate` restores the actor state and restarts any background services (invocations and timers) for active states, without re-executing entry actions.

---

## System Architecture & Concurrency

`gstate` uses a hybrid concurrency model to ensure safety and performance:

- **Sequential Mailbox (Channels)**: All events sent via `actor.Send(event)` are queued. A background goroutine processes them one by one, ensuring that state transitions and context updates are **strictly sequential**.
- **Thread-Safe Access (RWMutex)**: Methods like `actor.State()`, `actor.States()`, `actor.Context()`, and `actor.Snapshot()` are safe to call concurrently. They use a read-lock to provide a consistent view of the actor.
- **Asynchronous Integrity**: `Invoke` and `After` run in separate goroutines but their results are funneled back through the sequential logic to prevent data races on your Context.

---

## Background & Resources

`gstate` is based on the formalisms of Statecharts, which provide a rigorous way to model complex, event-driven systems.

- **[Statecharts: A Visual Formalism for Complex Systems](http://www.wisdom.weizmann.ac.il/~harel/papers/Statecharts.pdf)**: The original 1987 paper by David Harel that introduced the concept.
- **[W3C SCXML Specification](https://www.w3.org/TR/scxml/)**: The standard for State Chart XML, which defines many of the behaviors (like parallel states and history) implemented in this library.
- **[XState Documentation](https://xstate.js.org/docs/)**: An excellent resource for learning statechart patterns (the library that inspired `gstate`'s API).

## Detailed Examples

Check the [examples/](./examples) directory for runnable code with deep commentary on every feature:

| Example | Feature |
|---------|---------|
| [basics](./examples/basics) | States, events, transitions, entry/exit, assign |
| [hierarchy](./examples/hierarchy) | Nested states and event bubbling |
| [parallel](./examples/parallel) | Parallel regions and `States()` |
| [history](./examples/history) | Shallow and deep history |
| [invoke](./examples/invoke) | Async services with cancellation |
| [delayed](./examples/delayed) | Time-based transitions with `After` |
| [agent](./examples/agent) | Complex workflow with guards, always, and invoke |
| [persistence](./examples/persistence) | Snapshot serialization and hydration |

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
