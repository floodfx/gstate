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

### 2. Managing Data with Context (`Assign`)

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

## 3. Beyond Basics: Hierarchical (Nested) States

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

## 4. Orthogonal Logic: Parallel States

Sometimes a system is in multiple modes at once. A text editor might be `Focused` while also having `Bold` enabled.

Parallel states allow you to define regions that operate independently.

```go
s.Type(gstate.Parallel)
s.State("boldRegion", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Initial("off")
    s.State("off", ...)
    s.State("on", ...)
})
s.State("italicsRegion", func(s *gstate.StateBuilder[MyState, MyEvent, MyContext]) {
    s.Initial("off")
    s.State("off", ...)
    s.State("on", ...)
})
```

---

## 5. Side Effects: Invoke and After

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

## 6. Transient Logic (`Always`)

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

## System Architecture & Concurrency

`gstate` uses a hybrid concurrency model to ensure safety and performance:

- **Sequential Mailbox (Channels)**: All events sent via `actor.Send(event)` are queued. A background goroutine processes them one by one, ensuring that state transitions and context updates are **strictly sequential**.
- **Thread-Safe Access (RWMutex)**: Methods like `actor.State()` and `actor.Snapshot()` are safe to call concurrently. They use a read-lock to provide a consistent view of the actor.
- **Asynchronous Integrity**: `Invoke` and `After` run in separate goroutines but their results are funneled back through the sequential logic to prevent data races on your Context.

---

---

## Background & Resources

`gstate` is based on the formalisms of Statecharts, which provide a rigorous way to model complex, event-driven systems.

- **[Statecharts: A Visual Formalism for Complex Systems](http://www.wisdom.weizmann.ac.il/~harel/papers/Statecharts.pdf)**: The original 1987 paper by David Harel that introduced the concept.
- **[W3C SCXML Specification](https://www.w3.org/TR/scxml/)**: The standard for State Chart XML, which defines many of the behaviors (like parallel states and history) implemented in this library.
- **[XState Documentation](https://xstate.js.org/docs/)**: An excellent resource for learning statechart patterns (the library that inspired `gstate`'s API).

## Detailed Examples

Check the [examples/](./examples) directory for runnable code with deep commentary on every feature.
