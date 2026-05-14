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
//   - All callbacks except OnInvokeStarted and OnInvokeCompleted run
//     synchronously on the actor's event-processing goroutine while it holds
//     the actor's internal write lock. Implementations must be non-blocking.
//   - Implementations must not call methods on the same [Actor] that would
//     require re-entering the actor lock (e.g. [Actor.Snapshot], [Actor.State]).
//     They may call [Actor.Send] or [Actor.SendCtx], which only enqueue.
//   - OnInvokeStarted and OnInvokeCompleted fire from the invoke goroutine and
//     do not hold the actor lock.
//   - Payload pointer fields (Context *C) reference a defensive copy of the
//     actor's context taken at the moment the hook fires. Reading is safe and
//     accurately reflects state at that point; mutations on the pointee do
//     not affect the actor. If C implements [Cloner], that deep copy is used.
//
// To implement only a subset of the methods, embed [NopObserver].
type Observer[S ~string, E ~string, C any] interface {
	OnTransition(ctx context.Context, e TransitionEvent[S, E, C])
	OnGuardEvaluated(ctx context.Context, e GuardEvent[S, E, C])
	OnInvokeStarted(ctx context.Context, e InvokeEvent[S, E, C])
	OnInvokeCompleted(ctx context.Context, e InvokeEvent[S, E, C])
	OnStateEntered(ctx context.Context, e StateEvent[S, E, C])
	OnStateExited(ctx context.Context, e StateEvent[S, E, C])
	OnActionExecuted(ctx context.Context, e ActionEvent[S, E, C])
	OnEventReceived(ctx context.Context, e EventNotice[S, E, C])
	OnEventDropped(ctx context.Context, e EventNotice[S, E, C])
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
type TransitionEvent[S ~string, E ~string, C any] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	From      S       `json:"from"`
	To        S       `json:"to"`
	// Event is the triggering event. Zero value when the transition fires from
	// an Always, Delayed, or invoke-completion path.
	Event     E         `json:"event,omitempty"`
	Context   *C        `json:"context,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the transition as "transition[ActorID]: From --Event--> To".
// To is "<internal>" for transitions without a target state.
func (e TransitionEvent[S, E, C]) String() string {
	to := string(e.To)
	if to == "" {
		to = "<internal>"
	}
	return fmt.Sprintf("transition[%s]: %s --%s--> %s", e.ActorID, e.From, e.Event, to)
}

// GuardEvent is the payload for [Observer.OnGuardEvaluated]. It is emitted
// only when the transition defines a non-nil Guard, so the absence of an
// event does not imply the absence of guard evaluation.
type GuardEvent[S ~string, E ~string, C any] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	State     S       `json:"state"`
	// Event is the triggering event. Zero value for Always guards.
	Event     E         `json:"event,omitempty"`
	Target    S         `json:"target"`
	Result    bool      `json:"result"`
	Context   *C        `json:"context,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the guard as "guard[ActorID]: State --Event[Target]: result=true|false".
func (e GuardEvent[S, E, C]) String() string {
	return fmt.Sprintf("guard[%s]: %s --%s[%s]: result=%t", e.ActorID, e.State, e.Event, e.Target, e.Result)
}

// InvokeEvent is the payload for [Observer.OnInvokeStarted] and
// [Observer.OnInvokeCompleted].
type InvokeEvent[S ~string, E ~string, C any] struct {
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
func (e InvokeEvent[S, E, C]) String() string {
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
func (e InvokeEvent[S, E, C]) MarshalJSON() ([]byte, error) {
	type alias InvokeEvent[S, E, C]
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
type StateEvent[S ~string, E ~string, C any] struct {
	MachineID string    `json:"machine_id"`
	ActorID   ActorID   `json:"actor_id"`
	State     S         `json:"state"`
	Context   *C        `json:"context,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the state event as "state[ActorID]: State".
func (e StateEvent[S, E, C]) String() string {
	return fmt.Sprintf("state[%s]: %s", e.ActorID, e.State)
}

// ActionEvent is the payload for [Observer.OnActionExecuted]. It is emitted
// only when a transition has a non-nil Action.
type ActionEvent[S ~string, E ~string, C any] struct {
	MachineID string  `json:"machine_id"`
	ActorID   ActorID `json:"actor_id"`
	// State is the source state of the firing transition.
	State S `json:"state"`
	// Event is the triggering event. Zero value for Always / internal triggers.
	Event E `json:"event,omitempty"`
	// Target is the destination state ID, or zero for internal transitions.
	Target    S         `json:"target,omitempty"`
	Context   *C        `json:"context,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// String renders the action as "action[ActorID]: State --Event--> Target"
// (Target is "<internal>" when empty).
func (e ActionEvent[S, E, C]) String() string {
	to := string(e.Target)
	if to == "" {
		to = "<internal>"
	}
	return fmt.Sprintf("action[%s]: %s --%s--> %s", e.ActorID, e.State, e.Event, to)
}

// EventNotice is the payload for [Observer.OnEventReceived] and
// [Observer.OnEventDropped].
type EventNotice[S ~string, E ~string, C any] struct {
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
func (e EventNotice[S, E, C]) String() string {
	if e.Reason != "" {
		return fmt.Sprintf("event[%s]: %s reason=%s", e.ActorID, e.Event, e.Reason)
	}
	return fmt.Sprintf("event[%s]: %s", e.ActorID, e.Event)
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
type NopObserver[S ~string, E ~string, C any] struct{}

func (NopObserver[S, E, C]) OnTransition(context.Context, TransitionEvent[S, E, C])  {}
func (NopObserver[S, E, C]) OnGuardEvaluated(context.Context, GuardEvent[S, E, C])   {}
func (NopObserver[S, E, C]) OnInvokeStarted(context.Context, InvokeEvent[S, E, C])   {}
func (NopObserver[S, E, C]) OnInvokeCompleted(context.Context, InvokeEvent[S, E, C]) {}
func (NopObserver[S, E, C]) OnStateEntered(context.Context, StateEvent[S, E, C])     {}
func (NopObserver[S, E, C]) OnStateExited(context.Context, StateEvent[S, E, C])      {}
func (NopObserver[S, E, C]) OnActionExecuted(context.Context, ActionEvent[S, E, C])  {}
func (NopObserver[S, E, C]) OnEventReceived(context.Context, EventNotice[S, E, C])   {}
func (NopObserver[S, E, C]) OnEventDropped(context.Context, EventNotice[S, E, C])    {}

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
type RecordingObserver[S ~string, E ~string, C any] struct {
	NopObserver[S, E, C]
	mu     sync.Mutex
	events []RecordedEvent
}

func (r *RecordingObserver[S, E, C]) append(kind string, payload any, ts time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, RecordedEvent{Kind: kind, Payload: payload, Timestamp: ts})
}

func (r *RecordingObserver[S, E, C]) OnTransition(_ context.Context, e TransitionEvent[S, E, C]) {
	r.append(KindTransition, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnGuardEvaluated(_ context.Context, e GuardEvent[S, E, C]) {
	r.append(KindGuardEvaluated, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnInvokeStarted(_ context.Context, e InvokeEvent[S, E, C]) {
	r.append(KindInvokeStarted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnInvokeCompleted(_ context.Context, e InvokeEvent[S, E, C]) {
	r.append(KindInvokeCompleted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnStateEntered(_ context.Context, e StateEvent[S, E, C]) {
	r.append(KindStateEntered, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnStateExited(_ context.Context, e StateEvent[S, E, C]) {
	r.append(KindStateExited, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnActionExecuted(_ context.Context, e ActionEvent[S, E, C]) {
	r.append(KindActionExecuted, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnEventReceived(_ context.Context, e EventNotice[S, E, C]) {
	r.append(KindEventReceived, e, e.Timestamp)
}
func (r *RecordingObserver[S, E, C]) OnEventDropped(_ context.Context, e EventNotice[S, E, C]) {
	r.append(KindEventDropped, e, e.Timestamp)
}

// Events returns a copy of recorded events. If kinds are supplied, only entries
// whose Kind matches one of them are returned, in original order.
func (r *RecordingObserver[S, E, C]) Events(kinds ...string) []RecordedEvent {
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
func (r *RecordingObserver[S, E, C]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// Transitions returns every recorded [TransitionEvent].
func (r *RecordingObserver[S, E, C]) Transitions() []TransitionEvent[S, E, C] {
	return collect[TransitionEvent[S, E, C]](r, KindTransition)
}

// Guards returns every recorded [GuardEvent].
func (r *RecordingObserver[S, E, C]) Guards() []GuardEvent[S, E, C] {
	return collect[GuardEvent[S, E, C]](r, KindGuardEvaluated)
}

// StateEntered returns every recorded [StateEvent] for state entries.
func (r *RecordingObserver[S, E, C]) StateEntered() []StateEvent[S, E, C] {
	return collect[StateEvent[S, E, C]](r, KindStateEntered)
}

// StateExited returns every recorded [StateEvent] for state exits.
func (r *RecordingObserver[S, E, C]) StateExited() []StateEvent[S, E, C] {
	return collect[StateEvent[S, E, C]](r, KindStateExited)
}

// Actions returns every recorded [ActionEvent].
func (r *RecordingObserver[S, E, C]) Actions() []ActionEvent[S, E, C] {
	return collect[ActionEvent[S, E, C]](r, KindActionExecuted)
}

// InvokeStarted returns every recorded [InvokeEvent] for invoke starts.
func (r *RecordingObserver[S, E, C]) InvokeStarted() []InvokeEvent[S, E, C] {
	return collect[InvokeEvent[S, E, C]](r, KindInvokeStarted)
}

// InvokeCompleted returns every recorded [InvokeEvent] for invoke completions.
func (r *RecordingObserver[S, E, C]) InvokeCompleted() []InvokeEvent[S, E, C] {
	return collect[InvokeEvent[S, E, C]](r, KindInvokeCompleted)
}

// EventsReceived returns every recorded [EventNotice] for received events.
func (r *RecordingObserver[S, E, C]) EventsReceived() []EventNotice[S, E, C] {
	return collect[EventNotice[S, E, C]](r, KindEventReceived)
}

// EventsDropped returns every recorded [EventNotice] for dropped events.
func (r *RecordingObserver[S, E, C]) EventsDropped() []EventNotice[S, E, C] {
	return collect[EventNotice[S, E, C]](r, KindEventDropped)
}

func collect[T any, S ~string, E ~string, C any](r *RecordingObserver[S, E, C], kind string) []T {
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
