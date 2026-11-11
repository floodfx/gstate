package gstate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// pendingTimerCount reports how many delayed-transition timers the actor
// currently holds. White-box helper so tests can assert "no timer armed"
// without sleeping.
func pendingTimerCount[S ~string, E ~string, D Cloner[D]](a *Actor[S, E, D]) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	n := 0
	for _, ts := range a.timers {
		n += len(ts)
	}
	return n
}

func TestServiceActivationDisabledDefersInvokeAndAfter(t *testing.T) {
	var invokeRuns atomic.Int32
	invokeStarted := make(chan struct{}, 1)
	timedOut := make(chan struct{}, 1)

	m := New[StateID, EventID, Context]("deferred-services").
		Initial("work").
		State("work", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("job", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
					invokeRuns.Add(1)
					invokeStarted <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				}, "", "")
			})
			s.State("clock", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("ticking")
				s.State("ticking", func(s *StateBuilder[StateID, EventID, Context]) {
					s.After(time.Nanosecond).GoTo("expired")
				})
				s.State("expired", func(s *StateBuilder[StateID, EventID, Context]) {
					s.Entry(func(c Context) Context {
						timedOut <- struct{}{}
						return c
					})
				})
			})
		}).
		Build()

	actor := Start(m, Context{}, m.WithServiceActivation(false))
	defer actor.Stop()

	if got := invokeRuns.Load(); got != 0 {
		t.Fatalf("expected no invoke before Activate, got %d", got)
	}
	if got := pendingTimerCount(actor); got != 0 {
		t.Fatalf("expected no armed timers before Activate, got %d", got)
	}
	if !actor.Matches("ticking") {
		t.Fatalf("expected ticking to be active, got %v", actor.States())
	}

	actor.Activate()

	select {
	case <-invokeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invoke after Activate")
	}
	select {
	case <-timedOut:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for After transition after Activate")
	}

	// Activate is idempotent: a second call must not restart services.
	actor.Activate()
	if got := invokeRuns.Load(); got != 1 {
		t.Fatalf("expected exactly 1 invoke run, got %d", got)
	}
}

func TestServiceActivationSkipsStatesExitedBeforeActivate(t *testing.T) {
	var firstRuns, secondRuns atomic.Int32
	secondStarted := make(chan struct{}, 1)
	enteredB := make(chan struct{}, 1)

	m := New[StateID, EventID, Context]("deferred-exit").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				firstRuns.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
			s.After(time.Nanosecond).GoTo("timeout")
			s.On("NEXT").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context {
				enteredB <- struct{}{}
				return c
			})
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				secondRuns.Add(1)
				secondStarted <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
		}).
		State("timeout", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	actor := Start(m, Context{}, m.WithServiceActivation(false))
	defer actor.Stop()

	// Transitions still work while services are inactive.
	if err := actor.SendCtx(context.Background(), "NEXT"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-enteredB:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting to enter b")
	}
	if got := secondRuns.Load(); got != 0 {
		t.Fatalf("expected b's invoke deferred before Activate, got %d", got)
	}

	actor.Activate()
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for b's invoke after Activate")
	}
	if got := firstRuns.Load(); got != 0 {
		t.Fatalf("exited state a must never start its invoke, got %d runs", got)
	}
	if got := actor.State(); got != "b" {
		t.Fatalf("a's After must never fire after it exited, state=%q", got)
	}
	if got := pendingTimerCount(actor); got != 0 {
		t.Fatalf("expected no timers for exited state, got %d", got)
	}
}

func TestServiceActivationAfterActivateStartsNewStatesImmediately(t *testing.T) {
	started := make(chan struct{}, 1)
	m := New[StateID, EventID, Context]("activate-then-enter").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").GoTo("run")
		}).
		State("run", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
		}).
		Build()

	actor := Start(m, Context{}, m.WithServiceActivation(false))
	defer actor.Stop()
	actor.Activate()
	actor.Send("GO")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invoke of state entered after Activate")
	}
}

func TestServiceActivationDefaultIsActive(t *testing.T) {
	started := make(chan struct{}, 1)
	m := New[StateID, EventID, Context]("default-active").
		Initial("run").
		State("run", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				started <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
		}).
		Build()

	actor := Start(m, Context{}, m.WithServiceActivation(true))
	defer actor.Stop()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for invoke with activation enabled")
	}
	actor.Activate() // no-op on an already active actor
}

func TestServiceActivationActivateAfterStopIsNoop(t *testing.T) {
	var runs atomic.Int32
	m := New[StateID, EventID, Context]("activate-after-stop").
		Initial("run").
		State("run", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				runs.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
			s.After(time.Hour).GoTo("run")
		}).
		Build()

	actor := Start(m, Context{}, m.WithServiceActivation(false))
	actor.Stop()
	actor.Activate()
	<-actor.Done()
	if got := runs.Load(); got != 0 {
		t.Fatalf("Activate after Stop must not start services, got %d runs", got)
	}
	if got := pendingTimerCount(actor); got != 0 {
		t.Fatalf("Activate after Stop must not arm timers, got %d", got)
	}
}

func TestServiceActivationWithHydrate(t *testing.T) {
	var restoredRuns atomic.Int32
	restoredStarted := make(chan struct{}, 1)
	m := New[StateID, EventID, Context]("hydrate-deferred").
		Initial("idle").
		State("idle", func(*StateBuilder[StateID, EventID, Context]) {}).
		State("run", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				restoredRuns.Add(1)
				restoredStarted <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}, "", "")
		}).
		Build()

	snap := Snapshot[StateID, Context]{Active: []StateID{"run"}}

	t.Run("deferred until Activate", func(t *testing.T) {
		restoredRuns.Store(0)
		actor := Hydrate(m, snap, m.WithServiceActivation(false))
		defer actor.Stop()
		if got := restoredRuns.Load(); got != 0 {
			t.Fatalf("expected restored invoke deferred, got %d", got)
		}
		actor.Activate()
		select {
		case <-restoredStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for restored invoke after Activate")
		}
	})

	t.Run("WithHydrateServices(false) still skips restored states", func(t *testing.T) {
		restoredRuns.Store(0)
		actor := Hydrate(m, snap, m.WithServiceActivation(false), m.WithHydrateServices(false))
		actor.Activate()
		actor.Stop()
		if got := restoredRuns.Load(); got != 0 {
			t.Fatalf("expected restored invoke skipped, got %d", got)
		}
	})
}
