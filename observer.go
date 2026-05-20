package gstate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Observer is a hook surface for statechart lifecycle events. Implementations
// receive structured payloads when transitions fire, guards are evaluated,
// states are entered or exited, invoked services start and finish, transition
// actions run, and when events are received or dropped.
//
// The default observer is a no-op. To register one on an [Actor], pass
// [WithObserver] to [Start].
//
// Threading and locking contract:
//
//   - All callbacks except OnInvokeCompleted run synchronously on the actor's
//     event-processing goroutine while it holds the actor's internal write
//     lock. This includes OnInvokeStarted, which fires from
//     enterSingleState's service-restart path. Implementations must be
//     non-blocking.
//   - Implementations must not call methods on the same [Actor] that would
//     require re-entering the actor lock (e.g. [Actor.Snapshot],
//     [Actor.State]).
//   - Implementations must not call [Actor.Send] or [Actor.SendCtx]
//     synchronously: the channel send can block on a full mailbox, and the
//     loop goroutine that would drain it cannot acquire the actor lock the
//     observer is holding — a hard deadlock. If an observer needs to dispatch
//     into the actor, do it from a fresh goroutine
//     (e.g. `go func() { actor.Send(EventX) }()`).
//   - OnInvokeCompleted fires from the invoke goroutine and does not hold the
//     actor lock.
//   - Payload pointer fields (Context *C) reference a defensive copy of the
//     actor's context taken at the moment the hook fires. Reading is safe and
//     accurately reflects state at that point; mutations on the pointee do
//     not affect the actor. If C implements [Cloner], that deep copy is used.
//
// To implement only a subset of the methods, embed [NopObserver].
type Observer[S ~string, E ~string, D Cloner[D]] interface {
	OnTransition(ctx context.Context, e TransitionEvent[S, E, D])
	OnGuardEvaluated(ctx context.Context, e GuardEvent[S, E, D])
	OnInvokeStarted(ctx context.Context, e InvokeEvent[S, E, D])
	OnInvokeCompleted(ctx context.Context, e InvokeEvent[S, E, D])
	OnStateEntered(ctx context.Context, e StateEvent[S, E, D])
	OnStateExited(ctx context.Context, e StateEvent[S, E, D])
	OnActionExecuted(ctx context.Context, e ActionEvent[S, E, D])
	OnEventReceived(ctx context.Context, e EventNotice[S, E, D])
	OnEventDropped(ctx context.Context, e EventNotice[S, E, D])
}

// ActorID is the stable identifier for a running [Actor]. It is generated on
// [Start] (unless overridden via [WithActorID]) and survives [Actor.Snapshot]
// and [Hydrate] so telemetry can correlate across persistence boundaries.
type ActorID string

// Kind constants identify the entry type in [RecordedEvent.Kind] and are used
// to filter [RecordingObserver.Events].
const (
	KindTransition      = "transition"
	KindGuardEvaluated  = "guard"
	KindInvokeStarted   = "invoke_started"
	KindInvokeCompleted = "invoke_completed"
	KindStateEntered    = "state_entered"
	KindStateExited     = "state_exited"
	KindActionExecuted  = "action"
	KindEventReceived   = "event_received"
	KindEventDropped    = "event_dropped"
)

// TransitionEvent is the payload for [Observer.OnTransition].
type TransitionEvent[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	From      S       `json:"from"`
	To        S       `json:"to"`
	// Event is the triggering event. Zero value when the transition fires from
	// an Always, Delayed, or invoke-completion path.
	Event     E         `json:"event,omitempty"`
	Data      *D        `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the transition as "transition[ActorID]: From --Event--> To".
// To is "<internal>" for transitions without a target state.
func (e TransitionEvent[S, E, D]) String() string {
	to := string(e.To)
	if to == "" {
		to = "<internal>"
	}
	return fmt.Sprintf("transition[%s]: %s --%s--> %s", e.ActorID, e.From, e.Event, to)
}

// GuardEvent is the payload for [Observer.OnGuardEvaluated]. It is emitted
// only when the transition defines a non-nil Guard, so the absence of an
// event does not imply the absence of guard evaluation.
type GuardEvent[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	State     S       `json:"state"`
	// Event is the triggering event. Zero value for Always guards.
	Event     E         `json:"event,omitempty"`
	Target    S         `json:"target"`
	Result    bool      `json:"result"`
	Data      *D        `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the guard as "guard[ActorID]: State --Event[Target]: result=true|false".
func (e GuardEvent[S, E, D]) String() string {
	return fmt.Sprintf("guard[%s]: %s --%s[%s]: result=%t", e.ActorID, e.State, e.Event, e.Target, e.Result)
}

// InvokeEvent is the payload for [Observer.OnInvokeStarted] and
// [Observer.OnInvokeCompleted].
type InvokeEvent[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	State     S       `json:"state"`
	// Error is nil on OnInvokeStarted and on successful completion.
	// On cancellation it is typically [context.Canceled]. It serializes to
	// JSON as its string form (or null when nil) — see [InvokeEvent.MarshalJSON].
	Error error `json:"-"`
	// Duration is zero on OnInvokeStarted and the elapsed wall time on completion.
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
}

// String renders the invoke event as "invoke[ActorID]: state=... duration=... error=...".
// Fields with zero values are omitted.
func (e InvokeEvent[S, E, D]) String() string {
	parts := []string{fmt.Sprintf("invoke[%s]: state=%s", e.ActorID, e.State)}
	if e.Duration > 0 {
		parts = append(parts, fmt.Sprintf("duration=%s", e.Duration))
	}
	if e.Error != nil {
		parts = append(parts, fmt.Sprintf("error=%v", e.Error))
	}
	return strings.Join(parts, " ")
}

// MarshalJSON renders Error as its Error() string (or null when nil) so the
// payload round-trips through standard JSON tooling. All other fields use
// their declared json tags.
func (e InvokeEvent[S, E, D]) MarshalJSON() ([]byte, error) {
	type alias InvokeEvent[S, E, D]
	var errStr *string
	if e.Error != nil {
		s := e.Error.Error()
		errStr = &s
	}
	return json.Marshal(struct {
		alias
		Error *string `json:"error,omitempty"`
	}{alias(e), errStr})
}

// StateEvent is the payload for [Observer.OnStateEntered] and [Observer.OnStateExited].
type StateEvent[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string    `json:"machine_id"`
	ActorID   ActorID   `json:"actor_id"`
	State     S         `json:"state"`
	Data      *D        `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the state event as "state[ActorID]: State".
func (e StateEvent[S, E, D]) String() string {
	return fmt.Sprintf("state[%s]: %s", e.ActorID, e.State)
}

// ActionEvent is the payload for [Observer.OnActionExecuted]. It is emitted
// only when a transition has a non-nil Action.
type ActionEvent[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	// State is the source state of the firing transition.
	State S `json:"state"`
	// Event is the triggering event. Zero value for Always / internal triggers.
	Event E `json:"event,omitempty"`
	// Target is the destination state ID, or zero for internal transitions.
	Target    S         `json:"target,omitempty"`
	Data      *D        `json:"data,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the action as "action[ActorID]: State --Event--> Target"
// (Target is "<internal>" when empty).
func (e ActionEvent[S, E, D]) String() string {
	to := string(e.Target)
	if to == "" {
		to = "<internal>"
	}
	return fmt.Sprintf("action[%s]: %s --%s--> %s", e.ActorID, e.State, e.Event, to)
}

// EventNotice is the payload for [Observer.OnEventReceived] and
// [Observer.OnEventDropped].
type EventNotice[S ~string, E ~string, D Cloner[D]] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	Event     E       `json:"event"`
	// Reason describes why the event was dropped (e.g. "no_transition"). It is
	// empty on OnEventReceived.
	Reason    string    `json:"reason,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the event notice as "event[ActorID]: Event" or, when a
// reason is set (i.e. drop notices), "event[ActorID]: Event reason=...".
func (e EventNotice[S, E, D]) String() string {
	if e.Reason != "" {
		return fmt.Sprintf("event[%s]: %s reason=%s", e.ActorID, e.Event, e.Reason)
	}
	return fmt.Sprintf("event[%s]: %s", e.ActorID, e.Event)
}

// MultiObserver fans every [Observer] callback out to its underlying observers
// in order. It is useful when more than one consumer wants the lifecycle
// stream — for example, a [RecordingObserver] for tests alongside a custom
// observer that emits OpenTelemetry spans:
//
//	gstate.Start(m, ctx, m.WithObserver(
//	    gstate.MultiObserver[S, E, D]{logger, recorder, tracer},
//	))
//
// Members are dispatched in slice order; each member's callback completes
// before the next runs. MultiObserver inherits the threading contract on
// [Observer]: a slow or panicking member blocks the actor.
type MultiObserver[S ~string, E ~string, D Cloner[D]] []Observer[S, E, D]

func (m MultiObserver[S, E, D]) OnTransition(ctx context.Context, e TransitionEvent[S, E, D]) {
	for _, o := range m {
		o.OnTransition(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnGuardEvaluated(ctx context.Context, e GuardEvent[S, E, D]) {
	for _, o := range m {
		o.OnGuardEvaluated(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnInvokeStarted(ctx context.Context, e InvokeEvent[S, E, D]) {
	for _, o := range m {
		o.OnInvokeStarted(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnInvokeCompleted(ctx context.Context, e InvokeEvent[S, E, D]) {
	for _, o := range m {
		o.OnInvokeCompleted(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnStateEntered(ctx context.Context, e StateEvent[S, E, D]) {
	for _, o := range m {
		o.OnStateEntered(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnStateExited(ctx context.Context, e StateEvent[S, E, D]) {
	for _, o := range m {
		o.OnStateExited(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnActionExecuted(ctx context.Context, e ActionEvent[S, E, D]) {
	for _, o := range m {
		o.OnActionExecuted(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnEventReceived(ctx context.Context, e EventNotice[S, E, D]) {
	for _, o := range m {
		o.OnEventReceived(ctx, e)
	}
}
func (m MultiObserver[S, E, D]) OnEventDropped(ctx context.Context, e EventNotice[S, E, D]) {
	for _, o := range m {
		o.OnEventDropped(ctx, e)
	}
}

// SignalObserver returns an [Observer] whose every callback calls
// signal. The context and typed payload arguments are discarded — use
// [ObserverFuncs] if you need them, or embed [NopObserver] for a full
// custom implementation.
//
// signal must be non-blocking; observer callbacks run under the actor's
// write lock (see [Observer]'s threading contract). A nil signal makes
// the returned observer a no-op.
//
// Typical use: waking a channel when any lifecycle activity occurs.
//
//	ready := make(chan struct{}, 1)
//	obs := gstate.SignalObserver[MyState, MyEvent, MyContext](func() {
//	    select { case ready <- struct{}{}: default: }
//	})
//	actor := gstate.Start(machine, ctx, machine.WithObserver(obs))
//	actor.Send(EventGo)
//	<-ready
func SignalObserver[S ~string, E ~string, D Cloner[D]](signal func()) Observer[S, E, D] {
	return signalObserver[S, E, D]{signal: signal}
}

type signalObserver[S ~string, E ~string, D Cloner[D]] struct {
	signal func()
}

func (o signalObserver[S, E, D]) fire() {
	if o.signal != nil {
		o.signal()
	}
}

func (o signalObserver[S, E, D]) OnTransition(context.Context, TransitionEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnGuardEvaluated(context.Context, GuardEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnInvokeStarted(context.Context, InvokeEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnInvokeCompleted(context.Context, InvokeEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnStateEntered(context.Context, StateEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnStateExited(context.Context, StateEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnActionExecuted(context.Context, ActionEvent[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnEventReceived(context.Context, EventNotice[S, E, D]) {
	o.fire()
}
func (o signalObserver[S, E, D]) OnEventDropped(context.Context, EventNotice[S, E, D]) {
	o.fire()
}

// ObserverFuncs is a function-field adapter that implements [Observer]
// without forcing callers to embed [NopObserver] and override the
// callbacks they care about. Each lifecycle method dispatches first to
// AnyFunc (if non-nil), then to the kind-specific field (if non-nil);
// nil fields are no-ops.
//
//	obs := gstate.ObserverFuncs[MyState, MyEvent, MyContext]{
//	    AnyFunc:        func(ctx context.Context) { /* ... */ },
//	    TransitionFunc: func(ctx context.Context, e gstate.TransitionEvent[MyState, MyEvent, MyContext]) { /* ... */ },
//	}
//	actor := gstate.Start(machine, ctx, machine.WithObserver(obs))
//
// ObserverFuncs values are passed by value; the implementation uses
// value receivers. Do not mutate fields after installing on an actor.
// Callback bodies must be non-blocking — see [Observer]'s threading
// contract.
type ObserverFuncs[S ~string, E ~string, D Cloner[D]] struct {
	// AnyFunc fires for every lifecycle callback before the
	// kind-specific field (if any). Useful as a single "something
	// happened" hook for waiters and counters that still want the
	// originating context.
	AnyFunc func(context.Context)

	TransitionFunc      func(context.Context, TransitionEvent[S, E, D])
	GuardEvaluatedFunc  func(context.Context, GuardEvent[S, E, D])
	InvokeStartedFunc   func(context.Context, InvokeEvent[S, E, D])
	InvokeCompletedFunc func(context.Context, InvokeEvent[S, E, D])
	StateEnteredFunc    func(context.Context, StateEvent[S, E, D])
	StateExitedFunc     func(context.Context, StateEvent[S, E, D])
	ActionExecutedFunc  func(context.Context, ActionEvent[S, E, D])
	EventReceivedFunc   func(context.Context, EventNotice[S, E, D])
	EventDroppedFunc    func(context.Context, EventNotice[S, E, D])
}

func (o ObserverFuncs[S, E, D]) any(ctx context.Context) {
	if o.AnyFunc != nil {
		o.AnyFunc(ctx)
	}
}

func (o ObserverFuncs[S, E, D]) OnTransition(ctx context.Context, e TransitionEvent[S, E, D]) {
	o.any(ctx)
	if o.TransitionFunc != nil {
		o.TransitionFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnGuardEvaluated(ctx context.Context, e GuardEvent[S, E, D]) {
	o.any(ctx)
	if o.GuardEvaluatedFunc != nil {
		o.GuardEvaluatedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnInvokeStarted(ctx context.Context, e InvokeEvent[S, E, D]) {
	o.any(ctx)
	if o.InvokeStartedFunc != nil {
		o.InvokeStartedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnInvokeCompleted(ctx context.Context, e InvokeEvent[S, E, D]) {
	o.any(ctx)
	if o.InvokeCompletedFunc != nil {
		o.InvokeCompletedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnStateEntered(ctx context.Context, e StateEvent[S, E, D]) {
	o.any(ctx)
	if o.StateEnteredFunc != nil {
		o.StateEnteredFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnStateExited(ctx context.Context, e StateEvent[S, E, D]) {
	o.any(ctx)
	if o.StateExitedFunc != nil {
		o.StateExitedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnActionExecuted(ctx context.Context, e ActionEvent[S, E, D]) {
	o.any(ctx)
	if o.ActionExecutedFunc != nil {
		o.ActionExecutedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnEventReceived(ctx context.Context, e EventNotice[S, E, D]) {
	o.any(ctx)
	if o.EventReceivedFunc != nil {
		o.EventReceivedFunc(ctx, e)
	}
}
func (o ObserverFuncs[S, E, D]) OnEventDropped(ctx context.Context, e EventNotice[S, E, D]) {
	o.any(ctx)
	if o.EventDroppedFunc != nil {
		o.EventDroppedFunc(ctx, e)
	}
}

// NopObserver is a zero-cost [Observer] implementation whose methods do nothing.
// Embed it to implement only the callbacks you care about:
//
//	type myObs struct {
//	    gstate.NopObserver[MyState, MyEvent, MyContext]
//	}
//	func (o *myObs) OnTransition(ctx context.Context, e gstate.TransitionEvent[MyState, MyEvent, MyContext]) {
//	    // ...
//	}
type NopObserver[S ~string, E ~string, D Cloner[D]] struct{}

func (NopObserver[S, E, D]) OnTransition(context.Context, TransitionEvent[S, E, D])  {}
func (NopObserver[S, E, D]) OnGuardEvaluated(context.Context, GuardEvent[S, E, D])   {}
func (NopObserver[S, E, D]) OnInvokeStarted(context.Context, InvokeEvent[S, E, D])   {}
func (NopObserver[S, E, D]) OnInvokeCompleted(context.Context, InvokeEvent[S, E, D]) {}
func (NopObserver[S, E, D]) OnStateEntered(context.Context, StateEvent[S, E, D])     {}
func (NopObserver[S, E, D]) OnStateExited(context.Context, StateEvent[S, E, D])      {}
func (NopObserver[S, E, D]) OnActionExecuted(context.Context, ActionEvent[S, E, D])  {}
func (NopObserver[S, E, D]) OnEventReceived(context.Context, EventNotice[S, E, D])   {}
func (NopObserver[S, E, D]) OnEventDropped(context.Context, EventNotice[S, E, D])    {}

// RecordedEvent is one entry in a [RecordingObserver]'s log. Payload holds the
// matching typed payload struct (e.g. [TransitionEvent]); callers can either
// type-assert via Payload or use the typed accessors on [RecordingObserver].
type RecordedEvent struct {
	Kind      string    `json:"kind"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the entry as "Kind: Payload" using the payload's own String()
// method when it implements [fmt.Stringer], falling back to %+v otherwise.
func (r RecordedEvent) String() string {
	if s, ok := r.Payload.(fmt.Stringer); ok {
		return fmt.Sprintf("%s: %s", r.Kind, s.String())
	}
	return fmt.Sprintf("%s: %+v", r.Kind, r.Payload)
}

// RecordingObserver captures every callback into an in-memory log. It is safe
// for concurrent use and is intended both for tests and for ad-hoc live
// introspection of an actor's behavior.
//
// The recorder satisfies [Observer] by overriding every method on the embedded
// [NopObserver]; callers receive ordering identical to the engine's call
// sequence.
type RecordingObserver[S ~string, E ~string, D Cloner[D]] struct {
	NopObserver[S, E, D]
	mu     sync.Mutex
	events []RecordedEvent
}

func (r *RecordingObserver[S, E, D]) append(kind string, payload any, ts time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RecordedEvent{Kind: kind, Payload: payload, Timestamp: ts})
}

func (r *RecordingObserver[S, E, D]) OnTransition(_ context.Context, e TransitionEvent[S, E, D]) {
	r.append(KindTransition, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnGuardEvaluated(_ context.Context, e GuardEvent[S, E, D]) {
	r.append(KindGuardEvaluated, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnInvokeStarted(_ context.Context, e InvokeEvent[S, E, D]) {
	r.append(KindInvokeStarted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnInvokeCompleted(_ context.Context, e InvokeEvent[S, E, D]) {
	r.append(KindInvokeCompleted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnStateEntered(_ context.Context, e StateEvent[S, E, D]) {
	r.append(KindStateEntered, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnStateExited(_ context.Context, e StateEvent[S, E, D]) {
	r.append(KindStateExited, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnActionExecuted(_ context.Context, e ActionEvent[S, E, D]) {
	r.append(KindActionExecuted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnEventReceived(_ context.Context, e EventNotice[S, E, D]) {
	r.append(KindEventReceived, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, D]) OnEventDropped(_ context.Context, e EventNotice[S, E, D]) {
	r.append(KindEventDropped, e, e.Timestamp)
}

// Events returns a copy of recorded events. If kinds are supplied, only entries
// whose Kind matches one of them are returned, in original order.
func (r *RecordingObserver[S, E, D]) Events(kinds ...string) []RecordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(kinds) == 0 {
		out := make([]RecordedEvent, len(r.events))
		copy(out, r.events)
		return out
	}
	set := make(map[string]struct{}, len(kinds))
	for _, k := range kinds {
		set[k] = struct{}{}
	}
	out := make([]RecordedEvent, 0, len(r.events))
	for _, ev := range r.events {
		if _, ok := set[ev.Kind]; ok {
			out = append(out, ev)
		}
	}
	return out
}

// Reset clears the recorded log.
func (r *RecordingObserver[S, E, D]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// Transitions returns every recorded [TransitionEvent].
func (r *RecordingObserver[S, E, D]) Transitions() []TransitionEvent[S, E, D] {
	return collect[TransitionEvent[S, E, D]](r, KindTransition)
}

// Guards returns every recorded [GuardEvent].
func (r *RecordingObserver[S, E, D]) Guards() []GuardEvent[S, E, D] {
	return collect[GuardEvent[S, E, D]](r, KindGuardEvaluated)
}

// StateEntered returns every recorded [StateEvent] for state entries.
func (r *RecordingObserver[S, E, D]) StateEntered() []StateEvent[S, E, D] {
	return collect[StateEvent[S, E, D]](r, KindStateEntered)
}

// StateExited returns every recorded [StateEvent] for state exits.
func (r *RecordingObserver[S, E, D]) StateExited() []StateEvent[S, E, D] {
	return collect[StateEvent[S, E, D]](r, KindStateExited)
}

// Actions returns every recorded [ActionEvent].
func (r *RecordingObserver[S, E, D]) Actions() []ActionEvent[S, E, D] {
	return collect[ActionEvent[S, E, D]](r, KindActionExecuted)
}

// InvokeStarted returns every recorded [InvokeEvent] for invoke starts.
func (r *RecordingObserver[S, E, D]) InvokeStarted() []InvokeEvent[S, E, D] {
	return collect[InvokeEvent[S, E, D]](r, KindInvokeStarted)
}

// InvokeCompleted returns every recorded [InvokeEvent] for invoke completions.
func (r *RecordingObserver[S, E, D]) InvokeCompleted() []InvokeEvent[S, E, D] {
	return collect[InvokeEvent[S, E, D]](r, KindInvokeCompleted)
}

// EventsReceived returns every recorded [EventNotice] for received events.
func (r *RecordingObserver[S, E, D]) EventsReceived() []EventNotice[S, E, D] {
	return collect[EventNotice[S, E, D]](r, KindEventReceived)
}

// EventsDropped returns every recorded [EventNotice] for dropped events.
func (r *RecordingObserver[S, E, D]) EventsDropped() []EventNotice[S, E, D] {
	return collect[EventNotice[S, E, D]](r, KindEventDropped)
}

func collect[T any, S ~string, E ~string, D Cloner[D]](r *RecordingObserver[S, E, D], kind string) []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, 0)
	for _, ev := range r.events {
		if ev.Kind != kind {
			continue
		}
		if p, ok := ev.Payload.(T); ok {
			out = append(out, p)
		}
	}
	return out
}
