package gstate

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestSnapshotWithExcludesTransitionsAndOrdersActive(t *testing.T) {
	m := New[StateID, EventID, Context]("snapshot").Initial("parent").
		State("parent", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("idle")
			s.State("idle", func(s *StateBuilder[StateID, EventID, Context]) { s.On("NEXT").GoTo("next") })
			s.State("next", func(_ *StateBuilder[StateID, EventID, Context]) {})
		}).Build()
	transitioned := make(chan struct{}, 1)
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) { transitioned <- struct{}{} }}))
	defer a.Stop()
	send := make(chan struct{})
	enqueued := make(chan struct{})
	go func() { <-send; a.Send("NEXT"); close(enqueued) }()
	snap := a.SnapshotWith(func() {
		close(send)
		<-enqueued
		select {
		case <-transitioned:
			t.Error("transition ran inside snapshot callback")
		default:
		}
	})
	if !reflect.DeepEqual(snap.Active, []StateID{"parent", "idle"}) {
		t.Fatalf("active=%v", snap.Active)
	}
	select {
	case <-transitioned:
	case <-time.After(time.Second):
		t.Fatal("transition did not resume")
	}
	if !reflect.DeepEqual(a.Snapshot().Active, a.States()) {
		t.Fatal("snapshot ordering differs")
	}
}

func TestSnapshotWithPanicReleasesLock(t *testing.T) {
	m := New[StateID, EventID, Context]("panic").Initial("idle").State("idle", func(_ *StateBuilder[StateID, EventID, Context]) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected panic")
			}
		}()
		a.SnapshotWith(func() { panic("test") })
	}()
	a.Snapshot()
}

// Observer metadata and actor data must describe the same transition boundary.
func TestSnapshotWithObserverMetadata(t *testing.T) {
	var mu sync.Mutex
	var observed int
	completed := make(chan struct{}, 100)
	m := New[StateID, EventID, Context]("metadata").Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("STEP").Assign(func(d Context) Context { d.Count++; return d })
		}).Build()
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, Context]) {
		mu.Lock()
		observed++
		mu.Unlock()
		completed <- struct{}{}
	}}))
	defer a.Stop()
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for i := 0; i < 100; i++ {
			a.Send("STEP")
		}
	}()
	for i := 0; i < 100; i++ {
		var count int
		snap := a.SnapshotWith(func() { mu.Lock(); count = observed; mu.Unlock() })
		if count != snap.Data.Count {
			t.Fatalf("observer=%d snapshot=%d", count, snap.Data.Count)
		}
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("sender stalled")
	}
	for i := 0; i < 100; i++ {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("transition stalled")
		}
	}
}
