package gstate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPayloadStringers(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"transition", TransitionEvent[StateID, EventID, Context]{ActorID: "id1", From: "a", To: "b", Event: "GO"}.String(),
			"transition[id1]: a --GO--> b"},
		{"transition-internal", TransitionEvent[StateID, EventID, Context]{ActorID: "id1", From: "a", Event: "GO"}.String(),
			"transition[id1]: a --GO--> <internal>"},
		{"guard-true", GuardEvent[StateID, EventID, Context]{ActorID: "id1", State: "a", Event: "GO", Target: "b", Result: true}.String(),
			"guard[id1]: a --GO[b]: result=true"},
		{"guard-false", GuardEvent[StateID, EventID, Context]{ActorID: "id1", State: "a", Event: "GO", Target: "b", Result: false}.String(),
			"guard[id1]: a --GO[b]: result=false"},
		{"state", StateEvent[StateID, EventID, Context]{ActorID: "id1", State: "a"}.String(),
			"state[id1]: a"},
		{"action", ActionEvent[StateID, EventID, Context]{ActorID: "id1", State: "a", Event: "GO", Target: "b"}.String(),
			"action[id1]: a --GO--> b"},
		{"invoke-started", InvokeEvent[StateID, EventID, Context]{ActorID: "id1", State: "loading"}.String(),
			"invoke[id1]: state=loading"},
		{"invoke-completed", InvokeEvent[StateID, EventID, Context]{ActorID: "id1", State: "loading", Duration: 5 * time.Millisecond}.String(),
			"invoke[id1]: state=loading duration=5ms"},
		{"invoke-error", InvokeEvent[StateID, EventID, Context]{ActorID: "id1", State: "loading", Duration: 5 * time.Millisecond, Error: errors.New("boom")}.String(),
			"invoke[id1]: state=loading duration=5ms error=boom"},
		{"event-received", EventNotice[StateID, EventID, Context]{ActorID: "id1", Event: "GO"}.String(),
			"event[id1]: GO"},
		{"event-dropped", EventNotice[StateID, EventID, Context]{ActorID: "id1", Event: "GO", Reason: "no_transition"}.String(),
			"event[id1]: GO reason=no_transition"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestRecordedEventStringDelegates(t *testing.T) {
	r := RecordedEvent{
		Kind:    KindTransition,
		Payload: TransitionEvent[StateID, EventID, Context]{ActorID: "id1", From: "a", To: "b", Event: "GO"},
	}
	if got := r.String(); got != "transition: transition[id1]: a --GO--> b" {
		t.Errorf("RecordedEvent.String() = %q", got)
	}
}

func TestInvokeEventJSONRendersErrorAsString(t *testing.T) {
	e := InvokeEvent[StateID, EventID, Context]{
		MachineID: "m",
		ActorID:   "id1",
		State:     "loading",
		Duration:  3 * time.Millisecond,
		Error:     errors.New("boom"),
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"error":"boom"`) {
		t.Errorf("expected error string in JSON, got %s", s)
	}
	if !strings.Contains(s, `"state":"loading"`) {
		t.Errorf("expected state in JSON, got %s", s)
	}
}

func TestInvokeEventJSONOmitsNilError(t *testing.T) {
	e := InvokeEvent[StateID, EventID, Context]{ActorID: "id1", State: "loading"}
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), `"error"`) {
		t.Errorf("expected no error key when Error is nil, got %s", b)
	}
}

// TestRecordingObserverDirectCalls verifies the recorder appends one entry per
// callback and that both the typed accessors and the kind-filtered Events view
// return the same data.
func TestRecordingObserverDirectCalls(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	ctx := context.Background()
	c := Context{Count: 1}

	rec.OnEventReceived(ctx, EventNotice[StateID, EventID, Context]{Event: "GO"})
	rec.OnGuardEvaluated(ctx, GuardEvent[StateID, EventID, Context]{State: "a", Target: "b", Result: true})
	rec.OnStateExited(ctx, StateEvent[StateID, EventID, Context]{State: "a", Data: &c})
	rec.OnActionExecuted(ctx, ActionEvent[StateID, EventID, Context]{State: "a", Target: "b", Event: "GO"})
	rec.OnStateEntered(ctx, StateEvent[StateID, EventID, Context]{State: "b", Data: &c})
	rec.OnTransition(ctx, TransitionEvent[StateID, EventID, Context]{From: "a", To: "b", Event: "GO"})
	rec.OnInvokeStarted(ctx, InvokeEvent[StateID, EventID, Context]{State: "b"})
	rec.OnInvokeCompleted(ctx, InvokeEvent[StateID, EventID, Context]{State: "b", Duration: time.Millisecond})
	rec.OnEventDropped(ctx, EventNotice[StateID, EventID, Context]{Event: "NOPE", Reason: "no_transition"})

	all := rec.Events()
	if len(all) != 9 {
		t.Fatalf("expected 9 recorded events, got %d", len(all))
	}

	wantKinds := []string{
		KindEventReceived, KindGuardEvaluated, KindStateExited, KindActionExecuted,
		KindStateEntered, KindTransition, KindInvokeStarted, KindInvokeCompleted, KindEventDropped,
	}
	for i, want := range wantKinds {
		if all[i].Kind != want {
			t.Errorf("event %d: kind = %q, want %q", i, all[i].Kind, want)
		}
	}

	// Kind filter
	filtered := rec.Events(KindTransition, KindGuardEvaluated)
	if len(filtered) != 2 {
		t.Fatalf("filtered Events: got %d, want 2", len(filtered))
	}

	// Typed accessors
	if got := rec.Transitions(); len(got) != 1 || got[0].From != "a" || got[0].To != "b" {
		t.Errorf("Transitions(): unexpected %+v", got)
	}
	if got := rec.Guards(); len(got) != 1 || !got[0].Result {
		t.Errorf("Guards(): unexpected %+v", got)
	}
	if got := rec.StateEntered(); len(got) != 1 || got[0].State != "b" {
		t.Errorf("StateEntered(): unexpected %+v", got)
	}
	if got := rec.StateExited(); len(got) != 1 || got[0].State != "a" {
		t.Errorf("StateExited(): unexpected %+v", got)
	}
	if got := rec.Actions(); len(got) != 1 || got[0].Target != "b" {
		t.Errorf("Actions(): unexpected %+v", got)
	}
	if got := rec.InvokeStarted(); len(got) != 1 {
		t.Errorf("InvokeStarted(): unexpected %+v", got)
	}
	if got := rec.InvokeCompleted(); len(got) != 1 || got[0].Duration == 0 {
		t.Errorf("InvokeCompleted(): unexpected %+v", got)
	}
	if got := rec.EventsReceived(); len(got) != 1 || got[0].Event != "GO" {
		t.Errorf("EventsReceived(): unexpected %+v", got)
	}
	if got := rec.EventsDropped(); len(got) != 1 || got[0].Reason != "no_transition" {
		t.Errorf("EventsDropped(): unexpected %+v", got)
	}

	// Reset clears
	rec.Reset()
	if got := rec.Events(); len(got) != 0 {
		t.Errorf("after Reset: got %d events, want 0", len(got))
	}
}

// partialObs embeds NopObserver and overrides only OnTransition. This verifies
// the embedding-for-partial-implementation pattern from the plan.
type partialObs struct {
	NopObserver[StateID, EventID, Context]
	transitions int
}

func (p *partialObs) OnTransition(_ context.Context, _ TransitionEvent[StateID, EventID, Context]) {
	p.transitions++
}

func TestNopObserverEmbeddingPartialImpl(t *testing.T) {
	var obs Observer[StateID, EventID, Context] = &partialObs{}
	ctx := context.Background()

	// All nine methods must be callable without panic; only OnTransition records.
	obs.OnEventReceived(ctx, EventNotice[StateID, EventID, Context]{})
	obs.OnGuardEvaluated(ctx, GuardEvent[StateID, EventID, Context]{})
	obs.OnStateExited(ctx, StateEvent[StateID, EventID, Context]{})
	obs.OnActionExecuted(ctx, ActionEvent[StateID, EventID, Context]{})
	obs.OnStateEntered(ctx, StateEvent[StateID, EventID, Context]{})
	obs.OnTransition(ctx, TransitionEvent[StateID, EventID, Context]{})
	obs.OnTransition(ctx, TransitionEvent[StateID, EventID, Context]{})
	obs.OnInvokeStarted(ctx, InvokeEvent[StateID, EventID, Context]{})
	obs.OnInvokeCompleted(ctx, InvokeEvent[StateID, EventID, Context]{})
	obs.OnEventDropped(ctx, EventNotice[StateID, EventID, Context]{})

	p := obs.(*partialObs)
	if p.transitions != 2 {
		t.Errorf("OnTransition called %d times, want 2", p.transitions)
	}
}

// TestRecordingObserverConcurrent stresses the mutex-protected log.
func TestRecordingObserverConcurrent(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	const n = 100
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			rec.OnTransition(context.Background(), TransitionEvent[StateID, EventID, Context]{})
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		_ = rec.Events()
		<-done
	}
	if got := len(rec.Transitions()); got != n {
		t.Errorf("recorded %d transitions, want %d", got, n)
	}
}
