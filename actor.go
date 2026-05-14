package gstate

import (
	"context"
	"fmt"
	"sync"
	"time"

	nanoid "github.com/jaevor/go-nanoid"
)

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
type Actor[S ~string, E ~string, C any] struct {
	machine     *Machine[S, E, C]
	context     C
	active      map[S]bool
	history     map[S]S
	invocations map[S]context.CancelFunc
	timers      map[S][]*time.Timer
	mailbox     chan envelope[E]
	mu          sync.RWMutex
	stopOnce    sync.Once

	id       ActorID
	observer Observer[S, E, C]
}

// envelope carries an event together with the request-scoped context that
// originated it through the actor's mailbox.
type envelope[E ~string] struct {
	ctx   context.Context
	event E
}

// contextSnapshotPtr returns a pointer to a defensive copy of the actor's
// current context. It is used when building observer payloads so observers
// cannot accidentally mutate the actor's live state through a payload's
// Context pointer. When C implements [Cloner] the deep copy is used;
// otherwise Go's value-copy semantics suffice.
func (a *Actor[S, E, C]) contextSnapshotPtr() *C {
	if cloner, ok := any(a.context).(Cloner[C]); ok {
		c := cloner.Clone()
		return &c
	}
	c := a.context
	return &c
}

// config holds the resolved configuration for an [Actor]. It is built by
// applying [Option] values passed to [Start].
type config[S ~string, E ~string, C any] struct {
	mailboxSize int
	observer    Observer[S, E, C]
	actorID     ActorID
}

// Option configures an [Actor] at [Start] time. The provided helpers
// ([WithMailboxSize], [WithObserver], [WithActorID]) are the supported ways
// to construct one.
type Option[S ~string, E ~string, C any] func(*config[S, E, C])

// WithMailboxSize sets the buffered capacity of the actor's event channel.
// When omitted, the default is 100. Values <= 0 fall back to the default.
func WithMailboxSize[S ~string, E ~string, C any](n int) Option[S, E, C] {
	return func(c *config[S, E, C]) { c.mailboxSize = n }
}

// WithObserver installs an [Observer] that receives lifecycle callbacks for
// the actor. When omitted, a no-op observer is used. The observer is invoked
// synchronously on the actor's event-processing goroutine; see [Observer] for
// the full threading contract.
func WithObserver[S ~string, E ~string, C any](obs Observer[S, E, C]) Option[S, E, C] {
	return func(c *config[S, E, C]) { c.observer = obs }
}

// WithActorID overrides the auto-generated [ActorID] for the actor. When
// omitted, a fresh ID is generated with nanoid. Supplying an empty string is
// treated as "no override".
func WithActorID[S ~string, E ~string, C any](id ActorID) Option[S, E, C] {
	return func(c *config[S, E, C]) { c.actorID = id }
}

// Start creates and launches a new [Actor] for the given machine. Options are
// applied in order; later values for the same option win. The returned Actor
// is already running and ready to receive events via [Actor.Send] or
// [Actor.SendCtx].
func Start[S ~string, E ~string, C any](m *Machine[S, E, C], initialContext C, opts ...Option[S, E, C]) *Actor[S, E, C] {
	cfg := config[S, E, C]{mailboxSize: defaultMailboxSize}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.mailboxSize <= 0 {
		cfg.mailboxSize = defaultMailboxSize
	}
	if cfg.observer == nil {
		cfg.observer = NopObserver[S, E, C]{}
	}
	if cfg.actorID == "" {
		cfg.actorID = ActorID(idGen())
	}

	a := &Actor[S, E, C]{
		machine:     m,
		context:     initialContext,
		active:      make(map[S]bool),
		history:     make(map[S]S),
		invocations: make(map[S]context.CancelFunc),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan envelope[E], cfg.mailboxSize),
		id:          cfg.actorID,
		observer:    cfg.observer,
	}
	
	// Resolve all initial states (handling hierarchy and defaults)
	a.enterState(context.Background(), m.Initial)

	// Handle any transient transitions that fire immediately
	a.handleAlways(context.Background())
	
	go a.loop()
	return a
}

// Hydrate restores an [Actor] from a previously captured [Snapshot]. It
// restarts any services (invocations/timers) associated with the active states
// without re-executing state entry actions. The same [Option] set as [Start]
// is accepted so callers can attach an observer or tune the mailbox on a
// hydrated actor.
//
// The [ActorID] is resolved in priority order: [WithActorID] if supplied,
// otherwise the ActorID stored in the snapshot.
func Hydrate[S ~string, E ~string, C any](m *Machine[S, E, C], snapshot Snapshot[S, C], opts ...Option[S, E, C]) *Actor[S, E, C] {
	cfg := config[S, E, C]{mailboxSize: defaultMailboxSize}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.mailboxSize <= 0 {
		cfg.mailboxSize = defaultMailboxSize
	}
	if cfg.observer == nil {
		cfg.observer = NopObserver[S, E, C]{}
	}

	active := make(map[S]bool)
	for _, sID := range snapshot.Active {
		active[sID] = true
	}

	id := cfg.actorID
	if id == "" {
		id = snapshot.ActorID
	}

	a := &Actor[S, E, C]{
		machine:     m,
		context:     snapshot.Context,
		active:      active,
		history:     snapshot.History,
		invocations: make(map[S]context.CancelFunc),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan envelope[E], cfg.mailboxSize),
		id:          id,
		observer:    cfg.observer,
	}

	// Restart background services for all active states
	for sID := range active {
		a.restartServices(context.Background(), sID)
	}

	go a.loop()
	return a
}

// Stop stops the actor, cancels all running services/timers, and closes the mailbox.
func (a *Actor[S, E, C]) Stop() {
	a.stopOnce.Do(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		
		// Cancel all invocations
		if a.invocations != nil {
			for _, cancel := range a.invocations {
				cancel()
			}
			a.invocations = make(map[S]context.CancelFunc)
		}

		// Stop all timers
		if a.timers != nil {
			for _, timers := range a.timers {
				for _, t := range timers {
					t.Stop()
				}
			}
			a.timers = make(map[S][]*time.Timer)
		}

		if a.mailbox != nil {
			close(a.mailbox)
		}
	})
}

// ID returns the actor's stable identifier. The ID is generated on [Start]
// (unless overridden with [WithActorID]) and is preserved across
// [Actor.Snapshot] and [Hydrate] so telemetry can correlate the same logical
// actor across persistence boundaries.
func (a *Actor[S, E, C]) ID() ActorID {
	return a.id
}

// Context returns a thread-safe copy of the current context.
// If C implements Cloner[C], it returns the result of c.Clone().
func (a *Actor[S, E, C]) Context() C {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if cloner, ok := any(a.context).(Cloner[C]); ok {
		return cloner.Clone()
	}
	return a.context
}

// restartServices triggers the background tasks (invokes/timers) for a state.
// ctx is the request-scoped context that triggered entry into this state; it
// is currently unused inside the method but is plumbed through so future
// observer hooks (invoke start/complete in M6) can correlate.
func (a *Actor[S, E, C]) restartServices(ctx context.Context, id S) {
	_ = ctx
	stateDef := a.machine.States[id]
	if stateDef == nil { return }

	// Start or restart invoked services
	if stateDef.Invoke != nil {
		invokeCtx, cancel := context.WithCancel(context.Background())
		a.invocations[id] = cancel
		start := time.Now()
		a.observer.OnInvokeStarted(ctx, InvokeEvent[S, E, C]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     id,
			Timestamp: start,
		})
		go func(sID S, invoke *InvokeDef[S, E, C], c C) {
			err := invoke.Src(invokeCtx, c)
			completedAt := time.Now()
			// Report completion regardless of cancellation so observers can
			// see cancelled invocations. Use invokeCtx.Err() to surface the
			// cancellation reason when Src didn't return it explicitly.
			reportedErr := err
			if reportedErr == nil && invokeCtx.Err() != nil {
				reportedErr = invokeCtx.Err()
			}
			a.observer.OnInvokeCompleted(invokeCtx, InvokeEvent[S, E, C]{
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
				a.executeInternalTransition(invokeCtx, sID, target)
			}
		}(id, stateDef.Invoke, a.context)
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
func (a *Actor[S, E, C]) Snapshot() Snapshot[S, C] {
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

	ctx := a.context
	if cloner, ok := any(a.context).(Cloner[C]); ok {
		ctx = cloner.Clone()
	}

	return Snapshot[S, C]{
		Active:  active,
		History: history,
		Context: ctx,
		ActorID: a.id,
	}
}

// enterState handles the full entry logic for a state, including its children.
func (a *Actor[S, E, C]) enterState(ctx context.Context, id S) {
	a.enterSingleState(ctx, id)
	a.enterChildrenWithHistory(ctx, id, false)
}

// States returns the current list of all active states, ordered from root to leaf.
func (a *Actor[S, E, C]) States() []S {
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
func (a *Actor[S, E, C]) State() S {
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
func (a *Actor[S, E, C]) getSortedActiveStatesLocked() []S {
	res := make([]S, 0, len(a.active))
	for sID := range a.active {
		res = append(res, sID)
	}

	// Sort by depth descending (leaves first) using pre-computed depth
	for i := 0; i < len(res); i++ {
		for j := i + 1; j < len(res); j++ {
			dI := 0
			dJ := 0
			if sI, ok := a.machine.States[res[i]]; ok { dI = sI.depth }
			if sJ, ok := a.machine.States[res[j]]; ok { dJ = sJ.depth }
			
			if dI < dJ {
				res[i], res[j] = res[j], res[i]
			}
		}
	}
	return res
}

// Send enqueues an event in the actor's mailbox using [context.Background] as
// the request-scoped context. It is a non-blocking thin wrapper over
// [Actor.SendCtx].
func (a *Actor[S, E, C]) Send(event E) {
	a.SendCtx(context.Background(), event)
}

// SendCtx enqueues an event in the actor's mailbox carrying the supplied
// request-scoped context. The context is delivered to every [Observer]
// callback fired in response to this event, including Always transitions
// chained after it. Sending to a stopped actor is a no-op.
func (a *Actor[S, E, C]) SendCtx(ctx context.Context, event E) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		// Recover if sending to a closed mailbox after Stop.
		_ = recover()
	}()
	a.mailbox <- envelope[E]{ctx: ctx, event: event}
}

// loop is the main event loop running in a background goroutine.
func (a *Actor[S, E, C]) loop() {
	for env := range a.mailbox {
		a.handleEvent(env.ctx, env.event)
	}
}

func (a *Actor[S, E, C]) handleEvent(ctx context.Context, event E) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.observer.OnEventReceived(ctx, EventNotice[S, E, C]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		Event:     event,
		Timestamp: time.Now(),
	})

	activeStates := a.getSortedActiveStatesLocked()
	// Bubble up: check transitions from deepest state to root
	for _, sID := range activeStates {
		stateDef := a.machine.States[sID]
		if transitions, ok := stateDef.Transitions[event]; ok {
			for _, t := range transitions {
				if t.Guard != nil {
					ok := t.Guard(a.context)
					a.observer.OnGuardEvaluated(ctx, GuardEvent[S, E, C]{
						MachineID: a.machine.ID,
						ActorID:   a.id,
						State:     sID,
						Event:     event,
						Target:    t.Target,
						Result:    ok,
						Context:   a.contextSnapshotPtr(),
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
	a.observer.OnEventDropped(ctx, EventNotice[S, E, C]{
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
func (a *Actor[S, E, C]) executeTransition(ctx context.Context, sourceID S, t *TransitionDef[S, E, C], event E) {
	// 1. Handle internal transitions (no target)
	if t.Target == "" {
		if t.Action != nil {
			a.context = t.Action(a.context)
			a.observer.OnActionExecuted(ctx, ActionEvent[S, E, C]{
				MachineID: a.machine.ID,
				ActorID:   a.id,
				State:     sourceID,
				Event:     event,
				Target:    "",
				Context:   a.contextSnapshotPtr(),
				Timestamp: time.Now(),
			})
		}
		a.observer.OnTransition(ctx, TransitionEvent[S, E, C]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			From:      sourceID,
			To:        "",
			Event:     event,
			Context:   a.contextSnapshotPtr(),
			Timestamp: time.Now(),
		})
		return
	}

	targetState, ok := a.machine.States[t.Target]
	if !ok { return }
	sourceState, ok := a.machine.States[sourceID]
	if !ok { return }

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
				a.context = action(a.context)
			}
			if cancel, ok := a.invocations[sID]; ok {
				cancel()
				delete(a.invocations, sID)
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
		a.observer.OnStateExited(ctx, StateEvent[S, E, C]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     sID,
			Context:   a.contextSnapshotPtr(),
			Timestamp: time.Now(),
		})
	}

	// 3. Execute transition action (Assign)
	if t.Action != nil {
		a.context = t.Action(a.context)
		a.observer.OnActionExecuted(ctx, ActionEvent[S, E, C]{
			MachineID: a.machine.ID,
			ActorID:   a.id,
			State:     sourceID,
			Event:     event,
			Target:    t.Target,
			Context:   a.contextSnapshotPtr(),
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

	a.observer.OnTransition(ctx, TransitionEvent[S, E, C]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		From:      sourceID,
		To:        t.Target,
		Event:     event,
		Context:   a.contextSnapshotPtr(),
		Timestamp: time.Now(),
	})
}

// enterSingleState marks a state as active, runs its entry actions, and
// notifies the observer.
func (a *Actor[S, E, C]) enterSingleState(ctx context.Context, id S) {
	a.active[id] = true
	stateDef := a.machine.States[id]
	if stateDef == nil {
		return
	}

	for _, action := range stateDef.Entry {
		a.context = action(a.context)
	}

	a.observer.OnStateEntered(ctx, StateEvent[S, E, C]{
		MachineID: a.machine.ID,
		ActorID:   a.id,
		State:     id,
		Context:   a.contextSnapshotPtr(),
		Timestamp: time.Now(),
	})

	a.restartServices(ctx, id)
}

// executeInternalTransition handles transitions triggered by services (invokes/timers).
func (a *Actor[S, E, C]) executeInternalTransition(ctx context.Context, sourceID S, targetID S) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Verify the source state is still active before transitioning
	if !a.active[sourceID] {
		return
	}

	t := &TransitionDef[S, E, C]{Target: targetID}
	var zero E
	a.executeTransition(ctx, sourceID, t, zero)
	a.handleAlwaysInternal(ctx)
}

// enterChildrenWithHistory resolves the child states to enter, respecting history and parallel types.
func (a *Actor[S, E, C]) enterChildrenWithHistory(ctx context.Context, id S, deepHistory bool) {
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

// getPathToRoot returns the pre-computed slice of ancestor IDs for a state.
func (a *Actor[S, E, C]) getPathToRoot(id S) []S {
	if s, ok := a.machine.States[id]; ok {
		return s.path
	}
	return []S{}
}

// isDescendant returns true if childID is a descendant of parentID.
func (a *Actor[S, E, C]) isDescendant(childID, parentID S) bool {
	if parentID == "" { return true }
	curr := childID
	for curr != "" {
		if curr == parentID { return true }
		stateDef := a.machine.States[curr]
		if stateDef != nil {
			curr = stateDef.parent
		} else {
			break
		}
	}
	return false
}

// handleAlways acquires a lock and checks for transient transitions.
func (a *Actor[S, E, C]) handleAlways(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handleAlwaysInternal(ctx)
}

// handleAlwaysInternal checks active states for Always transitions.
// Must be called with a write lock.
// It includes a circuit breaker to prevent infinite loops.
func (a *Actor[S, E, C]) handleAlwaysInternal(ctx context.Context) {
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
			for _, t := range stateDef.Always {
				if t.Guard != nil {
					ok := t.Guard(a.context)
					a.observer.OnGuardEvaluated(ctx, GuardEvent[S, E, C]{
						MachineID: a.machine.ID,
						ActorID:   a.id,
						State:     sID,
						Event:     zero,
						Target:    t.Target,
						Result:    ok,
						Context:   a.contextSnapshotPtr(),
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
