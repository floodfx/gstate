package gstate

import (
	"context"
	"testing"
)

type counterObserver struct {
	BaseObserver[StateID, EventID, Context]
	transitions int
}

func (c *counterObserver) OnTransition(context.Context, *TransitionEvent[StateID, EventID, Context]) {
	c.transitions++
}

func TestWithObserversFansOutToAllMembers(t *testing.T) {
	a := &counterObserver{}
	b := &counterObserver{}

	m := tinyMachine()
	barrier := newKindBarrier(KindTransition, 1)
	actor := Start(m, Context{}, m.WithObservers(a, b, barrier))
	defer actor.Stop()

	actor.Send("GO")
	<-barrier.done

	if a.transitions != 1 || b.transitions != 1 {
		t.Errorf("expected both observers to receive transition call, got a=%d, b=%d", a.transitions, b.transitions)
	}
}

func TestWithObserversPreservesOrder(t *testing.T) {
	seen := []int{}
	makeRecorder := func(id int) Observer[StateID, EventID, Context] {
		return &orderRecorder{id: id, seen: &seen}
	}

	m := tinyMachine()
	barrier := newKindBarrier(KindTransition, 1)
	actor := Start(m, Context{}, m.WithObservers(makeRecorder(1), makeRecorder(2), makeRecorder(3), barrier))
	defer actor.Stop()

	actor.Send("GO")
	<-barrier.done

	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 3 {
		t.Errorf("expected order [1 2 3], got %v", seen)
	}
}

type orderRecorder struct {
	BaseObserver[StateID, EventID, Context]
	id   int
	seen *[]int
}

func (r *orderRecorder) OnTransition(context.Context, *TransitionEvent[StateID, EventID, Context]) {
	*r.seen = append(*r.seen, r.id)
}
