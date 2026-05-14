package gstate

import (
	"context"
	"testing"
)

// counterObserver tallies callbacks per kind so we can confirm fan-out reaches
// each member of a MultiObserver exactly once per source event.
type counterObserver struct {
	NopObserver[StateID, EventID, Context]
	transitions int
	guards      int
	entered     int
	exited      int
	actions     int
	received    int
	dropped     int
	started     int
	completed   int
}

func (c *counterObserver) OnTransition(context.Context, TransitionEvent[StateID, EventID, Context]) {
	c.transitions++
}
func (c *counterObserver) OnGuardEvaluated(context.Context, GuardEvent[StateID, EventID, Context]) {
	c.guards++
}
func (c *counterObserver) OnStateEntered(context.Context, StateEvent[StateID, EventID, Context]) {
	c.entered++
}
func (c *counterObserver) OnStateExited(context.Context, StateEvent[StateID, EventID, Context]) {
	c.exited++
}
func (c *counterObserver) OnActionExecuted(context.Context, ActionEvent[StateID, EventID, Context]) {
	c.actions++
}
func (c *counterObserver) OnEventReceived(context.Context, EventNotice[StateID, EventID, Context]) {
	c.received++
}
func (c *counterObserver) OnEventDropped(context.Context, EventNotice[StateID, EventID, Context]) {
	c.dropped++
}
func (c *counterObserver) OnInvokeStarted(context.Context, InvokeEvent[StateID, EventID, Context]) {
	c.started++
}
func (c *counterObserver) OnInvokeCompleted(context.Context, InvokeEvent[StateID, EventID, Context]) {
	c.completed++
}

func TestMultiObserverFansOutToAllMembers(t *testing.T) {
	a := &counterObserver{}
	b := &counterObserver{}
	c := &counterObserver{}

	multi := MultiObserver[StateID, EventID, Context]{a, b, c}
	ctx := context.Background()

	// Drive every method once.
	multi.OnTransition(ctx, TransitionEvent[StateID, EventID, Context]{})
	multi.OnGuardEvaluated(ctx, GuardEvent[StateID, EventID, Context]{})
	multi.OnStateEntered(ctx, StateEvent[StateID, EventID, Context]{})
	multi.OnStateExited(ctx, StateEvent[StateID, EventID, Context]{})
	multi.OnActionExecuted(ctx, ActionEvent[StateID, EventID, Context]{})
	multi.OnEventReceived(ctx, EventNotice[StateID, EventID, Context]{})
	multi.OnEventDropped(ctx, EventNotice[StateID, EventID, Context]{})
	multi.OnInvokeStarted(ctx, InvokeEvent[StateID, EventID, Context]{})
	multi.OnInvokeCompleted(ctx, InvokeEvent[StateID, EventID, Context]{})

	for i, obs := range []*counterObserver{a, b, c} {
		if obs.transitions != 1 || obs.guards != 1 || obs.entered != 1 ||
			obs.exited != 1 || obs.actions != 1 || obs.received != 1 ||
			obs.dropped != 1 || obs.started != 1 || obs.completed != 1 {
			t.Errorf("member %d: each method must fire once, got %+v", i, obs)
		}
	}
}

func TestMultiObserverSatisfiesObserverInterface(t *testing.T) {
	// Compile-time check: a MultiObserver value is a valid Observer.
	var _ Observer[StateID, EventID, Context] = MultiObserver[StateID, EventID, Context]{}
}

func TestMultiObserverPreservesOrder(t *testing.T) {
	// When dispatching to members in order, the first one's effects are visible
	// before the second's. Confirm with a shared counter so we can detect any
	// reordering.
	seen := []int{}
	makeRecorder := func(id int) Observer[StateID, EventID, Context] {
		return &orderRecorder{id: id, seen: &seen}
	}
	multi := MultiObserver[StateID, EventID, Context]{makeRecorder(1), makeRecorder(2), makeRecorder(3)}
	multi.OnTransition(context.Background(), TransitionEvent[StateID, EventID, Context]{})

	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Errorf("expected order [1 2 3], got %v", seen)
	}
}

type orderRecorder struct {
	NopObserver[StateID, EventID, Context]
	id   int
	seen *[]int
}

func (r *orderRecorder) OnTransition(context.Context, TransitionEvent[StateID, EventID, Context]) {
	*r.seen = append(*r.seen, r.id)
}

func TestMultiObserverEmptySliceIsNop(t *testing.T) {
	multi := MultiObserver[StateID, EventID, Context]{}
	// Must not panic on any method.
	multi.OnTransition(context.Background(), TransitionEvent[StateID, EventID, Context]{})
	multi.OnEventReceived(context.Background(), EventNotice[StateID, EventID, Context]{})
}
