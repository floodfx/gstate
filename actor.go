package gstate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	nanoid "github.com/jaevor/go-nanoid"
)

// ErrActorStopped is returned by [Actor.SendCtx] when the actor has already
// been stopped and the event cannot be delivered. It is distinct from a
// context-cancelled error so callers can branch on the reason an event
// was not delivered.
var ErrActorStopped = errors.New("gstate: actor stopped")

// defaultMailboxSize is the buffer size used when [WithMailboxSize] is not set.
const defaultMailboxSize = 100

// idGen is the package-level ActorID generator. It is initialized lazily and
// produces 12-character URL-safe identifiers.
var idGen = func() func() string {
	g, err := nanoid.Standard(12)
	if err != nil {
		// nanoid.Standard only errors on invalid length; 12 is valid.
		panic(fmt.Errorf("gstate: failed to initialize nanoid generator: %w", err))
	}
	return g
}()

// Actor is the runtime interpreter for a statechart machine.
// It maintains the current state, processes events sequentially,
// and manages asynchronous services (invocations and timers).
type Actor[S ~string, E ~string, D Cloner[D]] struct {
	machine     *Machine[S, E, D]
	data        D
	active      map[S]bool
	history     map[S]S
	invocations map[S]context.CancelFunc
	// invokeGens maps active state IDs to their active entry generation token.
	// This token ensures that a stale mutate closure spawned during a previous
	// state entry cannot apply mutations after the state has exited or re-entered.
	invokeGens  map[S]uint64
	// nextGen tracks the monotonic generation count for state entries.
	nextGen     uint64
	timers      map[S][]*time.Timer
	mailbox     chan envelope[E]
	mu          sync.RWMutex
	stopOnce    sync.Once
	// stopped is closed exactly once by Stop to signal shutdown to the loop
	// goroutine and to any caller parked in [Actor.SendCtx]'s select. Using a
	// dedicated channel (rather than closing the mailbox) avoids the
	// send-on-closed-channel panic when multiple senders race with Stop.
	stopped chan struct{}
	// wg tracks the loop goroutine and every invoke goroutine spawned by
	// [Actor.restartServices]. Stop waits on wg before returning so callers
	// can rely on a clean shutdown without leaked goroutines.
	wg sync.WaitGroup

	id       ActorID
	observer Observer[S, E, D]

	// sortedActive caches the result of getSortedActiveStatesLocked.
	// Nil means stale; rebuilt lazily on next call. Invalidated by
	// enterSingleState and exitState.
	sortedActive []S
}

// envelope carries an event together with the request-scoped context that
// originated it through the actor's mailbox.
type envelope[E ~string] struct {
	ctx   context.Context
	event E
}

// dataSnapshotPtr returns a pointer to a defensive copy of the actor's
// current data. It is used when building observer payloads so observers
// cannot accidentally mutate the actor's live state through a payload's
// Data pointer.
func (a *Actor[S, E, D]) dataSnapshotPtr() *D {
	c := a.data.Clone()
	return &c
}

// config holds the resolved configuration for an [Actor]. It is built by
// applying [Option] values passed to [Start].
type config[S ~string, E ~string, D Cloner[D]] struct {
	mailboxSize int
	observer    Observer[S, E, D]
	actorID     ActorID
}

// Option configures an [Actor] at [Start] or [Hydrate] time. Options are
// constructed via methods on a typed [Machine] — [Machine.WithMailboxSize],
// [Machine.WithObserver], [Machine.WithActorID] — which lets Go infer the
// [S, E, C] type parameters from the machine so the call site needs no
// annotations:
//
//	actor := gstate.Start(m, ctx,
//	    m.WithMailboxSize(500),
//	    m.WithObserver(obs),
//	    m.WithActorID("worker-42"),
//	)
type Option[S ~string, E ~string, D Cloner[D]] func(*config[S, E, D])

// WithMailboxSize returns an [Option] that sets the buffered capacity of the
// actor's event channel. When omitted, the default is 100. Values <= 0 fall
// back to the default.
func (m *Machine[S, E, D]) WithMailboxSize(n int) Option[S, E, D] {
	return func(c *config[S, E, D]) { c.mailboxSize = n }
}

// WithObserver returns an [Option] that installs an [Observer] receiving
// lifecycle callbacks for the actor. When omitted, a no-op observer is used.
// The observer is invoked synchronously on the actor's event-processing
// goroutine; see [Observer] for the full threading contract.
func (m *Machine[S, E, D]) WithObserver(obs Observer[S, E, D]) Option[S, E, D] {
	return func(c *config[S, E, D]) { c.observer = obs }
}

// WithActorID returns an [Option] that overrides the auto-generated [ActorID]
// for the actor. When omitted, a fresh ID is generated with nanoid. Supplying
// an empty string is treated as "no override".
func (m *Machine[S, E, D]) WithActorID(id ActorID) Option[S, E, D] {
	return func(c *config[S, E, D]) { c.actorID = id }
}

// Start creates and launches a new [Actor] for the given machine. Options are
// applied in order; later values for the same option win. The returned Actor
// is already running and ready to receive events via [Actor.Send] or
// [Actor.SendCtx].
func Start[S ~string, E ~string, D Cloner[D]](m *Machine[S, E, D], initialData D, opts ...Option[S, E, D]) *Actor[S, E, D] {
	cfg := config[S, E, D]{mailboxSize: defaultMailboxSize}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.mailboxSize <= 0 {
		cfg.mailboxSize = defaultMailboxSize
	}
	if cfg.observer == nil {
		cfg.observer = NopObserver[S, E, D]{}
	}
	if cfg.actorID == "" {
		cfg.actorID = ActorID(idGen())
	}

	a := &Actor[S, E, D]{
		machine:     m,
		data:        initialData,
		active:      make(map[S]bool),
		history:     make(map[S]S),
		invocations: make(map[S]context.CancelFunc),
		invokeGens:  make(map[S]uint64),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan envelope[E], cfg.mailboxSize),
		stopped:     make(chan struct{}),
		id:          cfg.actorID,
		observer:    cfg.observer,
	}

	// Resolve all initial states (handling hierarchy and defaults)
	a.mu.Lock()
	a.enterState(context.Background(), m.Initial)

	// Handle any transient transitions that fire immediately
	a.handleAlwaysInternal(context.Background())
	a.mu.Unlock()

	a.wg.Add(1)
	go a.loop()

	// If the Initial chain landed on a top-level "done" state, auto-stop
	// immediately. This handles machines whose Initial points directly
	// to a Final, or whose Always chain terminates in one.
	a.maybeAutoStop()
	return a
}

// Hydrate restores an [Actor] from a previously captured [Snapshot]. It
// restarts any services (invocations/timers) associated with the active states
// without re-executing state entry actions. The same [Option] set as [Start]
// is accepted so callers can attach an observer or tune the mailbox on a
// hydrated actor.
//
// Hydrate does not fire [Observer.OnStateEntered] or [Observer.OnTransition]
// for the states restored from the snapshot — those events represent the
// original state changes that were already observed before the snapshot was
// captured. Hooks resume firing on the next event, Always evaluation, or
// invoke completion handled by the hydrated actor.
//
// The [ActorID] is resolved in priority order: [WithActorID] if supplied,
// otherwise the ActorID stored in the snapshot.
func Hydrate[S ~string, E ~string, D Cloner[D]](m *Machine[S, E, D], snapshot Snapshot[S, D], opts ...Option[S, E, D]) *Actor[S, E, D] {
	cfg := config[S, E, D]{mailboxSize: defaultMailboxSize}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.mailboxSize <= 0 {
		cfg.mailboxSize = defaultMailboxSize
	}
	if cfg.observer == nil {
		cfg.observer = NopObserver[S, E, D]{}
	}

	active := make(map[S]bool)
	for _, sID := range snapshot.Active {
		active[sID] = true
	}

	// Snapshots produced by Actor.Snapshot always have a non-nil
	// History map, but JSON unmarshal can produce nil when the field
	// is missing (e.g. `{"active":[...]}`). Coalesce so subsequent
	// writes in executeTransition don't panic.
	history := snapshot.History
	if history == nil {
		history = make(map[S]S)
	}

	id := cfg.actorID
	if id == "" {
		id = snapshot.ActorID
	}

	a := &Actor[S, E, D]{
		machine:     m,
		data:        snapshot.Data,
		active:      active,
		history:     history,
		invocations: make(map[S]context.CancelFunc),
		invokeGens:  make(map[S]uint64),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan envelope[E], cfg.mailboxSize),
		stopped:     make(chan struct{}),
		id:          id,
		observer:    cfg.observer,
	}

	// Restart background services for all active states
	a.mu.Lock()
	for sID := range active {
		a.restartServices(context.Background(), sID)
	}
	a.mu.Unlock()

	a.wg.Add(1)
	go a.loop()

	// If the hydrated snapshot is already in a "done" state, auto-stop.
	a.maybeAutoStop()
	return a
}

// isStateDoneLocked returns true if the subtree rooted at sID has reached
// its completion state per SCXML semantics. Must be called with a.mu held
// (read or write).
//
//   - Final: trivially done.
//   - Atomic (non-Final): never done.
//   - Compound: done iff its currently-active child is done.
//   - Parallel: done iff every region is done.
func (a *Actor[S, E, D]) isStateDoneLocked(sID S) bool {
	sd := a.machine.States[sID]
	if sd == nil {
		return false
	}
	switch sd.Type {
	case Final:
		return true
	case Atomic:
		return false
	case Compound:
		// Exactly one child of a compound state is active.
		for childID := range sd.States {
			if a.active[childID] {
				return a.isStateDoneLocked(childID)
			}
		}
		return false
	case Parallel:
		if len(sd.States) == 0 {
			return false
		}
		for childID := range sd.States {
			if !a.isStateDoneLocked(childID) {
				return false
			}
		}
		return true
	}
	return false
}

// maybeAutoStop spawns Stop on a fresh goroutine if the machine's top-level
// active state has reached completion (see isStateDoneLocked).
//
// Stop must run on a separate goroutine: when maybeAutoStop is triggered
// from the loop goroutine (after handleEvent returns), a synchronous Stop
// would deadlock — Stop both re-acquires a.mu and waits on a.wg for the
// loop goroutine to exit, which is the goroutine that called Stop.
//
// Must be called WITHOUT holding a.mu; it acquires RLock briefly to read
// the active set.
func (a *Actor[S, E, D]) maybeAutoStop() {
	a.mu.RLock()
	var topLevel S
	var found bool
	for sID := range a.active {
		sd := a.machine.States[sID]
		if sd != nil && sd.parent == "" {
			topLevel = sID
			found = true
			break
		}
	}
	done := found && a.isStateDoneLocked(topLevel)
	a.mu.RUnlock()
	if done {
		go a.Stop()
	}
}

// Stop shuts the actor down and waits for all goroutines it owns to exit
// before returning. It is safe to call from multiple goroutines and safe to
// call more than once — only the first call performs the shutdown work.
//
// Stop completion contract:
//
// Guaranteed finished before Stop returns:
//
//   - Entry, exit, and transition actions, and guard evaluations, for any
//     event the actor had already begun processing. These run synchronously
//     under the actor's write lock; Stop acquires that lock before signalling
//     shutdown, so the in-flight transition completes first.
//   - [InvokeDef] Func goroutines. Stop cancels their [context.Context] and
//     waits for each Func function to return.
//   - [Observer.OnInvokeCompleted] callbacks for each in-flight or cancelled
//     invoke. They fire from inside the invoke goroutine immediately before
//     the WaitGroup decrement.
//   - [Observer.OnStateExited] and [Observer.OnStateEntered] callbacks
//     fired during the in-flight transition.
//
// Not awaited by Stop:
//
//   - Events buffered in the mailbox that the actor had not yet pulled.
//     They are abandoned: once Stop has signalled shutdown, the loop exits
//     without consuming further events. Use the invoke pattern below if
//     you have work that must run before shutdown.
//   - Goroutines spawned by user code from inside an action, guard, or
//     invoke (for example `go publishMetric(c)` inside an Assign action).
//     The actor has no handle on them. If the work must finish before Stop
//     returns, model it as an [InvokeDef] Func instead — Stop awaits invoke
//     goroutines.
//   - [time.AfterFunc] callbacks for delayed transitions that were already
//     firing when Stop ran. They safely no-op via the inactive-state check
//     in executeInternalTransition but Stop does not block for them.
//
// Send after Stop is a no-op. [Actor.Send] and [Actor.SendCtx] called after
// Stop (or concurrently with Stop past the shutdown-signal point) return
// without delivering the event and without panicking.
func (a *Actor[S, E, D]) Stop() {
	a.stopOnce.Do(func() {
		a.mu.Lock()
		// Cancel all invocations
		for _, cancel := range a.invocations {
			cancel()
		}
		a.invocations = make(map[S]context.CancelFunc)
		a.invokeGens = make(map[S]uint64)

		// Stop all timers
		for _, timers := range a.timers {
			for _, t := range timers {
				t.Stop()
			}
		}
		a.timers = make(map[S][]*time.Timer)

		// Signal shutdown. Senders parked in SendCtx's select wake on the
		// stopped channel and return without delivering. The loop goroutine
		// observes stopped at its next iteration and exits.
		close(a.stopped)
		a.mu.Unlock()

		// Wait for the loop goroutine and every invoke goroutine to return
		// before declaring shutdown complete.
		a.wg.Wait()
	})
}

// ID returns the actor's stable identifier. The ID is generated on [Start]
// (unless overridden with [WithActorID]) and is preserved across
// [Actor.Snapshot] and [Hydrate] so telemetry can correlate the same logical
// actor across persistence boundaries.
func (a *Actor[S, E, D]) ID() ActorID {
	return a.id
}

// Data returns a thread-safe copy of the current data.
// Thread safety is guaranteed by calling [Cloner.Clone] on the underlying data.
func (a *Actor[S, E, D]) Data() D {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.data.Clone()
}

// restartServices triggers the background tasks (invokes/timers) for a state.
// ctx is the request-scoped context that triggered entry into this state. It
// is fired through OnInvokeStarted and is the parent of the cancellable
// invokeCtx passed to invoke.Func, so any trace/span values on ctx propagate
// to Func as well as to OnInvokeCompleted. Cancelling ctx cancels the invoke.
func (a *Actor[S, E, D]) restartServices(ctx context.Context, id S) {
	stateDef := a.machine.States[id]
	if stateDef == nil {
		return
	}

	// Start or restart invoked services
	if stateDef.Invoke != nil {
		invokeCtx, cancel := context.WithCancel(ctx)
		a.invocations[id] = cancel

		// Monotonically increment the generation token for this new state entry.
		// Any mutate closures spawned for this invocation will capture this 'gen'
		// value and will only commit mutations if this generation matches the
		// currently active state entry generation in invokeGens.
		a.nextGen++
		gen := a.nextGen
		a.invokeGens[id] = gen

		snap := a.data.Clone()
		start := time.Now()

		mutate := func(fn func(D) D) {
			a.mu.Lock()
			defer a.mu.Unlock()
			select {
			case <-a.stopped:
				return
			default:
			}
			// Reject mutations if the state has exited or if a new entry has 
			// superseded this invoke's generation (e.g. during A -> B -> A cycling).
			if a.invokeGens[id] != gen {
				return
			}
			a.data = fn(a.data)
		}

		a.observer.OnInvokeStarted(ctx, InvokeEvent[S, E, D]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     id,
			Timestamp: start,
		})
		a.wg.Add(1)
		go func(sID S, invoke *InvokeDef[S, E, D], snap D, mutate func(func(D) D)) {
			defer a.wg.Done()
			err := invoke.Func(invokeCtx, snap, mutate)
			completedAt := time.Now()
			// Report completion regardless of cancellation so observers can
			// see cancelled invocations. Use invokeCtx.Err() to surface the
			// cancellation reason when Func didn't return it explicitly.
			reportedErr := err
			if reportedErr == nil && invokeCtx.Err() != nil {
				reportedErr = invokeCtx.Err()
			}
			a.observer.OnInvokeCompleted(invokeCtx, InvokeEvent[S, E, D]{
				MachineID: a.machine.ID,
				ActorID:   a.id,
				State:     sID,
				Error:     reportedErr,
				Duration:  completedAt.Sub(start),
				Timestamp: completedAt,
			})
			if invokeCtx.Err() != nil {
				return // cancelled by state exit
			}
			target := invoke.OnDone
			if err != nil {
				target = invoke.OnError
			}
			if target != "" {
				a.executeInternalTransition(context.WithoutCancel(invokeCtx), sID, target)
			}
		}(id, stateDef.Invoke, snap, mutate)
	}

	// Start or restart delayed transitions
	for _, t := range stateDef.Delayed {
		trans := t // capture for closure
		timer := time.AfterFunc(t.After, func() {
			a.executeInternalTransition(context.Background(), id, trans.Target)
		})
		a.timers[id] = append(a.timers[id], timer)
	}
}

// Snapshot captures the current status of the Actor, including its active states,
// history data, and context. The returned struct is suitable for JSON serialization.
func (a *Actor[S, E, D]) Snapshot() Snapshot[S, D] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	active := make([]S, 0, len(a.active))
	for sID := range a.active {
		active = append(active, sID)
	}

	history := make(map[S]S)
	for k, v := range a.history {
		history[k] = v
	}

	ctx := a.data.Clone()

	return Snapshot[S, D]{
		Active:  active,
		History: history,
		Data:    ctx,
		ActorID: a.id,
	}
}

// enterState handles the full entry logic for a state, including its children.
func (a *Actor[S, E, D]) enterState(ctx context.Context, id S) {
	a.enterSingleState(ctx, id)
	a.enterChildrenWithHistory(ctx, id, false)
}

// States returns the current list of all active states, ordered from root to leaf.
func (a *Actor[S, E, D]) States() []S {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// Get states sorted by depth (deepest first)
	active := a.getSortedActiveStatesLocked()

	// Reverse to get root-to-leaf order
	res := make([]S, len(active))
	for i, sID := range active {
		res[len(active)-1-i] = sID
	}
	return res
}

// State returns the ID of the current deepest active leaf state.
// In the case of parallel states, it returns one of the active leaf states.
func (a *Actor[S, E, D]) State() S {
	a.mu.RLock()
	defer a.mu.RUnlock()
	active := a.getSortedActiveStatesLocked()
	if len(active) > 0 {
		return active[0] // deepest leaf
	}
	return ""
}

// getSortedActiveStatesLocked retrieves active states sorted by depth (deepest first).
// Must be called with at least a read lock.
func (a *Actor[S, E, D]) getSortedActiveStatesLocked() []S {
	if a.sortedActive != nil {
		return a.sortedActive
	}

	res := make([]S, 0, len(a.active))
	for sID := range a.active {
		res = append(res, sID)
	}

	// Sort by depth descending (leaves first) using pre-computed depth.
	// Tie-break alphabetically DESCENDING on state ID so that when States()
	// reverses the slice, equal-depth states are alphabetically ASCENDING.
	slices.SortFunc(res, func(aStr, bStr S) int {
		dI := 0
		dJ := 0
		if sI, ok := a.machine.States[aStr]; ok {
			dI = sI.depth
		}
		if sJ, ok := a.machine.States[bStr]; ok {
			dJ = sJ.depth
		}

		if dI != dJ {
			return dJ - dI
		}
		if aStr < bStr {
			return 1
		}
		if aStr > bStr {
			return -1
		}
		return 0
	})
	a.sortedActive = res
	return res
}

// Send enqueues an event in the actor's mailbox using [context.Background]
// as the request-scoped context. It is a thin wrapper over [Actor.SendCtx]
// that discards the returned error; with context.Background the only
// possible non-nil return is [ErrActorStopped], in which case the send
// is a no-op per Stop's contract (no panic, no delivery).
//
// Callers that need to react to delivery failure should use [Actor.SendCtx]
// directly.
func (a *Actor[S, E, D]) Send(event E) {
	_ = a.SendCtx(context.Background(), event)
}

// SendCtx enqueues an event in the actor's mailbox carrying the supplied
// request-scoped context. The context is threaded into every [Observer]
// callback fired in response to this event (including Always transitions
// chained after it) AND it gates the enqueue itself.
//
// Returns:
//   - nil when the event was enqueued.
//   - ctx.Err() ([context.Canceled] or [context.DeadlineExceeded]) when
//     the supplied context was cancelled or its deadline elapsed before
//     the event could be enqueued. The event is NOT delivered.
//   - [ErrActorStopped] when the actor was stopped before the event could
//     be enqueued. The event is NOT delivered.
//
// Behaviour when the mailbox is full: SendCtx blocks until one of three
// things happens — a slot opens, ctx is done, or the actor is stopped.
// It never blocks forever.
func (a *Actor[S, E, D]) SendCtx(ctx context.Context, event E) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Fast path: bail before enqueueing if ctx or actor is already done.
	// Without this, the inner select could pick the mailbox-send case
	// even when ctx is already cancelled (select chooses fairly among
	// ready cases) and we'd deliver an event whose ctx is dead.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stopped:
		return ErrActorStopped
	default:
	}
	select {
	case a.mailbox <- envelope[E]{ctx: ctx, event: event}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stopped:
		return ErrActorStopped
	}
}

// loop is the main event loop running in a background goroutine. It exits
// when [Actor.Stop] closes a.stopped. The priority pre-check makes the exit
// deterministic: once stopped is closed, the next iteration returns without
// pulling any further buffered events from the mailbox.
func (a *Actor[S, E, D]) loop() {
	defer a.wg.Done()
	for {
		// Priority: bail out if Stop has signalled shutdown, even if events
		// remain buffered. Stop's contract is "abandon, don't drain."
		select {
		case <-a.stopped:
			return
		default:
		}
		select {
		case env := <-a.mailbox:
			a.handleEvent(env.ctx, env.event)
			// After the lock released, check whether the transition
			// landed us in a "done" top-level state and auto-stop if so.
			a.maybeAutoStop()
		case <-a.stopped:
			return
		}
	}
}

func (a *Actor[S, E, D]) handleEvent(ctx context.Context, event E) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.observer.OnEventReceived(ctx, EventNotice[S, E, D]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		Event:     event,
		Timestamp: time.Now(),
	})

	activeStates := a.getSortedActiveStatesLocked()
	// Bubble up: check transitions from deepest state to root
	for _, sID := range activeStates {
		stateDef := a.machine.States[sID]
		if stateDef == nil {
			// Hydrate accepts arbitrary Active state IDs; ignore any
			// that don't correspond to a state in this machine.
			continue
		}
		if transitions, ok := stateDef.Transitions[event]; ok {
			for _, t := range transitions {
				if t.Guard != nil {
					ok := t.Guard(a.data)
					a.observer.OnGuardEvaluated(ctx, GuardEvent[S, E, D]{
						MachineID: a.machine.ID,
						ActorID:   a.id,
						State:     sID,
						Event:     event,
						Target:    t.Target,
						Result:    ok,
						Data:      a.dataSnapshotPtr(),
						Timestamp: time.Now(),
					})
					if !ok {
						continue
					}
				}
				a.executeTransition(ctx, sID, t, event)
				// Check for transient transitions after the event transition
				a.handleAlwaysInternal(ctx)
				return
			}
		}
	}

	// No transition fired for this event.
	a.observer.OnEventDropped(ctx, EventNotice[S, E, D]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		Event:     event,
		Reason:    "no_transition",
		Timestamp: time.Now(),
	})
}

// executeTransition performs the state transition logic, including LCA resolution,
// entry/exit actions, and context updates. ctx is the request-scoped context
// for the triggering event (or context.Background() for internal triggers).
// event is the triggering event ID (zero value for Always/Delayed/Invoke).
func (a *Actor[S, E, D]) executeTransition(ctx context.Context, sourceID S, t *TransitionDef[S, E, D], event E) {
	// 1. Handle internal transitions (no target)
	if t.Target == "" {
		if t.Action != nil {
			a.data = t.Action(a.data)
			a.observer.OnActionExecuted(ctx, ActionEvent[S, E, D]{
				MachineID: a.machine.ID,
				ActorID:   a.id,
				State:     sourceID,
				Event:     event,
				Target:    "",
				Data:      a.dataSnapshotPtr(),
				Timestamp: time.Now(),
			})
		}
		a.observer.OnTransition(ctx, TransitionEvent[S, E, D]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			From:      sourceID,
			To:        "",
			Event:     event,
			Data:      a.dataSnapshotPtr(),
			Timestamp: time.Now(),
		})
		return
	}

	targetState, ok := a.machine.States[t.Target]
	if !ok {
		return
	}
	sourceState, ok := a.machine.States[sourceID]
	if !ok {
		return
	}

	targetPath := targetState.path
	sourcePath := sourceState.path

	// Find Lowest Common Ancestor (LCA) to determine which states to exit/enter
	lcaID := S("")
	for i := 0; i < len(sourcePath) && i < len(targetPath); i++ {
		if sourcePath[i] == targetPath[i] {
			lcaID = sourcePath[i]
		} else {
			break
		}
	}

	// 2. Exit states from deepest active up to but not including LCA
	toExit := []S{}
	sortedActive := a.getSortedActiveStatesLocked()

	// FIX: Self-transition behavior.
	isSelfTransition := sourceID == t.Target

	for _, sID := range sortedActive {
		if isSelfTransition {
			if sID == sourceID || a.isDescendant(sID, sourceID) {
				toExit = append(toExit, sID)
			}
		} else {
			// Standard LCA logic: Exit if descendant of LCA and NOT the LCA itself
			if sID != lcaID && a.isDescendant(sID, lcaID) {
				toExit = append(toExit, sID)
			}
		}
	}

	for _, sID := range toExit {
		stateDef := a.machine.States[sID]
		if stateDef != nil {
			for _, action := range stateDef.Exit {
				a.data = action(a.data)
			}
			if cancel, ok := a.invocations[sID]; ok {
				cancel()
				delete(a.invocations, sID)
				delete(a.invokeGens, sID)
			}
			if ts, ok := a.timers[sID]; ok {
				for _, timer := range ts {
					timer.Stop()
				}
				delete(a.timers, sID)
			}
			if stateDef.parent != "" {
				a.history[stateDef.parent] = sID
			}
		}
		delete(a.active, sID)
		a.sortedActive = nil // invalidate cache
		a.observer.OnStateExited(ctx, StateEvent[S, E, D]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     sID,
			Data:      a.dataSnapshotPtr(),
			Timestamp: time.Now(),
		})
	}

	// 3. Execute transition action (Assign)
	if t.Action != nil {
		a.data = t.Action(a.data)
		a.observer.OnActionExecuted(ctx, ActionEvent[S, E, D]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     sourceID,
			Event:     event,
			Target:    t.Target,
			Data:      a.dataSnapshotPtr(),
			Timestamp: time.Now(),
		})
	}

	// 4. Enter states from LCA (exclusive) down to target
	if isSelfTransition {
		a.enterSingleState(ctx, t.Target)
		a.enterChildrenWithHistory(ctx, t.Target, false)
	} else {
		lcaDepth := -1
		if lcaID != "" {
			for i, id := range targetPath {
				if id == lcaID {
					lcaDepth = i
					break
				}
			}
		}

		for i := lcaDepth + 1; i < len(targetPath); i++ {
			a.enterSingleState(ctx, targetPath[i])
		}

		// 5. Resolve children of the target state
		if len(targetPath) > 0 {
			a.enterChildrenWithHistory(ctx, targetPath[len(targetPath)-1], false)
		}
	}

	a.observer.OnTransition(ctx, TransitionEvent[S, E, D]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		From:      sourceID,
		To:        t.Target,
		Event:     event,
		Data:      a.dataSnapshotPtr(),
		Timestamp: time.Now(),
	})
}

// enterSingleState marks a state as active, runs its entry actions, and
// notifies the observer.
func (a *Actor[S, E, D]) enterSingleState(ctx context.Context, id S) {
	a.active[id] = true
	a.sortedActive = nil // invalidate cache
	stateDef := a.machine.States[id]
	if stateDef == nil {
		return
	}

	for _, action := range stateDef.Entry {
		a.data = action(a.data)
	}

	a.observer.OnStateEntered(ctx, StateEvent[S, E, D]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		State:     id,
		Data:      a.dataSnapshotPtr(),
		Timestamp: time.Now(),
	})

	a.restartServices(ctx, id)
}

// executeInternalTransition handles transitions triggered by services (invokes/timers).
func (a *Actor[S, E, D]) executeInternalTransition(ctx context.Context, sourceID S, targetID S) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Verify the source state is still active before transitioning
	if !a.active[sourceID] {
		return
	}

	t := &TransitionDef[S, E, D]{Target: targetID}
	var zero E
	a.executeTransition(ctx, sourceID, t, zero)
	a.handleAlwaysInternal(ctx)
}

// enterChildrenWithHistory resolves the child states to enter, respecting history and parallel types.
func (a *Actor[S, E, D]) enterChildrenWithHistory(ctx context.Context, id S, deepHistory bool) {
	stateDef := a.machine.States[id]
	if stateDef == nil {
		return
	}

	if stateDef.Type == Parallel {
		// Parallel regions: enter all regions
		for childID := range stateDef.States {
			a.enterSingleState(ctx, childID)
			a.enterChildrenWithHistory(ctx, childID, deepHistory)
		}
	} else {
		// Compound regions: determine which child to enter
		targetID := stateDef.Initial
		useDeepHistory := deepHistory || stateDef.History == Deep

		if (stateDef.History == Shallow || stateDef.History == Deep) && a.history[id] != "" {
			targetID = a.history[id]
		} else if deepHistory && a.history[id] != "" {
			targetID = a.history[id]
		}

		if targetID != "" {
			a.enterSingleState(ctx, targetID)
			a.enterChildrenWithHistory(ctx, targetID, useDeepHistory)
		}
	}
}

// isDescendant returns true if childID is a descendant of parentID.
func (a *Actor[S, E, D]) isDescendant(childID, parentID S) bool {
	if parentID == "" {
		return true
	}
	curr := childID
	for curr != "" {
		if curr == parentID {
			return true
		}
		stateDef := a.machine.States[curr]
		if stateDef != nil {
			curr = stateDef.parent
		} else {
			break
		}
	}
	return false
}


// handleAlwaysInternal checks active states for Always transitions.
// Must be called with a write lock.
// It includes a circuit breaker to prevent infinite loops.
func (a *Actor[S, E, D]) handleAlwaysInternal(ctx context.Context) {
	const maxIterations = 100
	iterations := 0
	var zero E

	for {
		iterations++
		if iterations > maxIterations {
			// Panic or log error? For a library, panic might be too harsh, but infinite loop is fatal.
			// Ideally we return an error, but this is an internal void method.
			// We will break to save the process.
			fmt.Printf("gstate: infinite loop detected in Always transitions (max %d)\n", maxIterations)
			break
		}

		transitioned := false
		sortedActive := a.getSortedActiveStatesLocked()

		for _, sID := range sortedActive {
			stateDef := a.machine.States[sID]
			if stateDef == nil {
				// Hydrate accepts arbitrary Active state IDs; ignore any
				// that don't correspond to a state in this machine.
				continue
			}
			for _, t := range stateDef.Always {
				if t.Guard != nil {
					ok := t.Guard(a.data)
					a.observer.OnGuardEvaluated(ctx, GuardEvent[S, E, D]{
						MachineID: a.machine.ID,
						ActorID:   a.id,
						State:     sID,
						Event:     zero,
						Target:    t.Target,
						Result:    ok,
						Data:      a.dataSnapshotPtr(),
						Timestamp: time.Now(),
					})
					if !ok {
						continue
					}
				}
				a.executeTransition(ctx, sID, t, zero)
				transitioned = true
				break // restart loop to re-evaluate active states
			}
			if transitioned {
				break
			}
		}

		if !transitioned {
			break
		}
	}
}
