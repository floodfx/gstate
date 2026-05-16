package gstate

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// Compile-time assertion that ObserverFuncs implements Observer.
var _ Observer[StateID, EventID, Context] = ObserverFuncs[StateID, EventID, Context]{}

// TestObserverFuncsTypedOnlyFires sets only TransitionFunc and asserts
// it receives the right payload. Other callbacks fire on the actor but
// no field is set for them, so they're silent.
func TestObserverFuncsTypedOnlyFires(t *testing.T) {
	got := make(chan TransitionEvent[StateID, EventID, Context], 1)
	obs := ObserverFuncs[StateID, EventID, Context]{
		TransitionFunc: func(_ context.Context, e TransitionEvent[StateID, EventID, Context]) {
			got <- e
		},
	}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{obs, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	select {
	case e := <-got:
		if e.From != "a" || e.To != "b" || e.Event != "GO" {
			t.Fatalf("TransitionFunc payload = %+v, want from=a to=b event=GO", e)
		}
	default:
		t.Fatal("TransitionFunc was not called")
	}
}

// TestObserverFuncsAnyFiresForEveryCallback sets only AnyFunc and
// asserts it fires for every callback the actor produces. Compared
// against a RecordingObserver in the same MultiObserver for ground
// truth.
func TestObserverFuncsAnyFiresForEveryCallback(t *testing.T) {
	var count atomic.Int64
	obs := ObserverFuncs[StateID, EventID, Context]{
		AnyFunc: func(context.Context) { count.Add(1) },
	}
	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{obs, rec, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got, want := count.Load(), int64(len(rec.Events())); got != want {
		t.Fatalf("AnyFunc count %d != recorder event count %d", got, want)
	}
}

// TestObserverFuncsAnyFiresBeforeKindSpecific records the order of
// AnyFunc vs TransitionFunc calls. AnyFunc must precede the
// kind-specific call for the same event.
func TestObserverFuncsAnyFiresBeforeKindSpecific(t *testing.T) {
	var mu sync.Mutex
	var order []string

	obs := ObserverFuncs[StateID, EventID, Context]{
		AnyFunc: func(context.Context) {
			mu.Lock()
			order = append(order, "any")
			mu.Unlock()
		},
		TransitionFunc: func(_ context.Context, _ TransitionEvent[StateID, EventID, Context]) {
			mu.Lock()
			order = append(order, "transition")
			mu.Unlock()
		},
	}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{obs, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	mu.Lock()
	defer mu.Unlock()

	// Find the "transition" entry; the immediately-preceding entry must
	// be "any" (the AnyFunc call paired with the OnTransition dispatch).
	idx := -1
	for i, s := range order {
		if s == "transition" {
			idx = i
			break
		}
	}
	if idx < 1 {
		t.Fatalf("no transition entry found in order=%v", order)
	}
	if order[idx-1] != "any" {
		t.Fatalf("entry before transition = %q, want any (order=%v)", order[idx-1], order)
	}
}

// TestObserverFuncsNilFieldsArentInvoked sets only EventReceivedFunc
// and confirms no panic when other callbacks fire. EventReceivedFunc
// must still receive its event.
func TestObserverFuncsNilFieldsArentInvoked(t *testing.T) {
	got := make(chan EventNotice[StateID, EventID, Context], 1)
	obs := ObserverFuncs[StateID, EventID, Context]{
		EventReceivedFunc: func(_ context.Context, e EventNotice[StateID, EventID, Context]) {
			got <- e
		},
	}
	bar := newKindBarrier(KindTransition, 1)

	m := guardedMachine(true)
	a := Start(m, Context{}, m.WithObserver(
		MultiObserver[StateID, EventID, Context]{obs, bar},
	))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	select {
	case e := <-got:
		if e.Event != "GO" {
			t.Fatalf("EventReceivedFunc got event=%q, want GO", e.Event)
		}
	default:
		t.Fatal("EventReceivedFunc was not called")
	}
}
