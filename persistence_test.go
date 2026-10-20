package gstate

import (
	"context"
	"slices"
	"sync"
	"testing"
)

func TestActorPersistence(t *testing.T) {
	m := New[StateID, EventID, Context]("persist").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("active")
		}).
		State("active", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context {
				c.Count++
				return c
			})
			s.On("STOP").GoTo("idle")
		}).
		Build()

	bar1 := newKindBarrier(KindTransition, 1)
	actor := Start(m, Context{Count: 0}, m.WithObservers(bar1))
	defer actor.Stop()

	actor.Send("START")
	<-bar1.done

	if actor.State() != "active" {
		t.Errorf("Expected state active, got %s", actor.State())
	}
	if actor.Snapshot().Data.Count != 1 {
		t.Errorf("Expected count 1, got %d", actor.Snapshot().Data.Count)
	}

	snapshot := actor.Snapshot()

	// Create new actor from snapshot
	bar2 := newKindBarrier(KindTransition, 1)
	actor2 := Hydrate(m, snapshot, m.WithObservers(bar2))
	defer actor2.Stop()

	if actor2.State() != "active" {
		t.Errorf("Expected hydrated state active, got %s", actor2.State())
	}
	if actor2.Snapshot().Data.Count != 1 {
		t.Errorf("Expected hydrated count 1, got %d", actor2.Snapshot().Data.Count)
	}

	actor2.Send("STOP")
	<-bar2.done
	if actor2.State() != "idle" {
		t.Errorf("Expected state idle after hydrated STOP, got %s", actor2.State())
	}
}

func TestHydrateOwnsHistoryAndData(t *testing.T) {
	m := New[string, string, *CloneCtx]("history-own").
		Initial("p").
		State("p", func(s *StateBuilder[string, string, *CloneCtx]) {
			s.Initial("a")
			s.History(Shallow)
			s.State("a", func(s *StateBuilder[string, string, *CloneCtx]) {
				s.On("GO").Assign(func(c *CloneCtx) *CloneCtx {
					c.Value++
					c.Data[0] = 100
					return c
				}).GoTo("b")
			})
			s.State("b", func(s *StateBuilder[string, string, *CloneCtx]) {
				s.On("OUT").Assign(func(c *CloneCtx) *CloneCtx {
					c.Value += 10
					c.Data[0] = 200
					return c
				}).GoTo("out")
			})
		}).
		State("out", func(_ *StateBuilder[string, string, *CloneCtx]) {}).
		Build()

	origHistory := map[string]string{"p": "a"}
	origData := &CloneCtx{Value: 1, Data: []int{10}}
	snap := Snapshot[string, *CloneCtx]{
		Active:  []string{"p", "b"},
		History: origHistory,
		Data:    origData,
	}

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	a1 := Hydrate(m, snap, m.WithObservers(ObserverFuncs[string, string, *CloneCtx]{
		TransitionFunc: func(context.Context, *TransitionEvent[string, string, *CloneCtx]) { close(done1) },
	}))
	defer a1.Stop()
	a2 := Hydrate(m, snap, m.WithObservers(ObserverFuncs[string, string, *CloneCtx]{
		TransitionFunc: func(context.Context, *TransitionEvent[string, string, *CloneCtx]) { close(done2) },
	}))
	defer a2.Stop()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		a1.Send("OUT")
		<-done1
	}()
	go func() {
		defer wg.Done()
		a2.Send("OUT")
		<-done2
	}()
	wg.Wait()

	// Caller's snapshot history and data must not be mutated.
	if origHistory["p"] != "a" {
		t.Errorf("caller snapshot history mutated to %q, want \"a\"", origHistory["p"])
	}
	if origData.Value != 1 || origData.Data[0] != 10 {
		t.Errorf("caller snapshot data mutated to %+v, want Value=1 Data=[10]", origData)
	}
	if a1.Snapshot().History["p"] != "b" || a2.Snapshot().History["p"] != "b" {
		t.Errorf("hydrated actors did not update their own history independently")
	}
}

func TestHydrateNormalizesAncestorsAndRestartsDeterministically(t *testing.T) {
	m := New[StateID, EventID, Context]("hydrate-norm").
		Initial("root").
		State("root", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("child")
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
			s.On("BUBBLE").GoTo("done")
			s.State("child", func(cs *StateBuilder[StateID, EventID, Context]) {
				cs.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
					<-ctx.Done()
					return ctx.Err()
				}, "", "")
			})
		}).
		State("done", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Final)
		}).
		Build()

	for i := 0; i < 10; i++ {
		var started []StateID
		var mu sync.Mutex
		obs := ObserverFuncs[StateID, EventID, Context]{
			InvokeStartedFunc: func(_ context.Context, e *InvokeEvent[StateID, EventID, Context]) {
				mu.Lock()
				started = append(started, e.State)
				mu.Unlock()
			},
		}

		// Supply only leaf state "child" plus an unknown state "bogus".
		// Hydrate must close over ancestor "root", ignore "bogus", and
		// restart services in deterministic root-to-leaf order: [root, child].
		a := Hydrate(m, Snapshot[StateID, Context]{
			Active: []StateID{"child", "bogus"},
		}, m.WithObservers(obs))

		states := a.States()
		wantStates := []StateID{"root", "child"}
		if !slices.Equal(states, wantStates) {
			a.Stop()
			t.Fatalf("iter %d: States() = %v, want %v", i, states, wantStates)
		}

		mu.Lock()
		gotStarted := slices.Clone(started)
		mu.Unlock()
		if !slices.Equal(gotStarted, wantStates) {
			a.Stop()
			t.Fatalf("iter %d: InvokeStarted order = %v, want %v", i, gotStarted, wantStates)
		}

		// Because ancestor "root" is active, "BUBBLE" transitions to "done" (Final) and auto-stops.
		a.Send("BUBBLE")
		<-a.stopped
		a.Stop()
	}
}
