package gstate

import (
	"context"
	"sync"
)

// kindBarrier is a test-only observer that closes a channel after seeing a
// target number of events of a given kind. It replaces wall-clock polling /
// time.Sleep with a deterministic synchronisation primitive: tests do
//
//	bar := newKindBarrier(KindTransition, 1)
//	a := Start(m, ctx, m.WithObservers(rec, bar))
//	a.Send("GO")
//	<-bar.done
//
// to block exactly until the actor has observed the expected lifecycle event.
type kindBarrier struct {
	BaseObserver[StateID, EventID, Context]
	mu   sync.Mutex
	kind string
	want int
	seen int
	done chan struct{}
}

func newKindBarrier(kind string, n int) *kindBarrier {
	return &kindBarrier{kind: kind, want: n, done: make(chan struct{})}
}

func (b *kindBarrier) tick(kind string) {
	if kind != b.kind {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen++
	if b.seen == b.want {
		close(b.done)
	}
}

func (b *kindBarrier) OnTransition(context.Context, *TransitionEvent[StateID, EventID, Context]) {
	b.tick(KindTransition)
}
func (b *kindBarrier) OnGuardEvaluated(context.Context, *GuardEvent[StateID, EventID, Context]) {
	b.tick(KindGuardEvaluated)
}
func (b *kindBarrier) OnInvokeStarted(context.Context, *InvokeEvent[StateID, EventID, Context]) {
	b.tick(KindInvokeStarted)
}
func (b *kindBarrier) OnInvokeCompleted(context.Context, *InvokeEvent[StateID, EventID, Context]) {
	b.tick(KindInvokeCompleted)
}
func (b *kindBarrier) OnStateEntered(context.Context, *StateEvent[StateID, EventID, Context]) {
	b.tick(KindStateEntered)
}
func (b *kindBarrier) OnStateExited(context.Context, *StateEvent[StateID, EventID, Context]) {
	b.tick(KindStateExited)
}
func (b *kindBarrier) OnActionExecuted(context.Context, *ActionEvent[StateID, EventID, Context]) {
	b.tick(KindActionExecuted)
}
func (b *kindBarrier) OnEventReceived(context.Context, *EventNotice[StateID, EventID, Context]) {
	b.tick(KindEventReceived)
}
func (b *kindBarrier) OnEventDropped(context.Context, *EventNotice[StateID, EventID, Context]) {
	b.tick(KindEventDropped)
}
