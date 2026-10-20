package gstate

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// mutatingObserver attempts to mutate Data through every payload it sees.
// If dataSnapshotPtr does its job, the actor's own data is unaffected.
type mutatingObserver struct {
	BaseObserver[StateID, EventID, Context]
}

func (mutatingObserver) OnStateEntered(_ context.Context, e *StateEvent[StateID, EventID, Context]) {
	if e.Data() != nil {
		e.Data().Count = 9999
	}
}
func (mutatingObserver) OnStateExited(_ context.Context, e *StateEvent[StateID, EventID, Context]) {
	if e.Data() != nil {
		e.Data().Count = 9999
	}
}
func (mutatingObserver) OnTransition(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
	if e.Data() != nil {
		e.Data().Count = 9999
	}
}
func (mutatingObserver) OnGuardEvaluated(_ context.Context, e *GuardEvent[StateID, EventID, Context]) {
	if e.Data() != nil {
		e.Data().Count = 9999
	}
}
func (mutatingObserver) OnActionExecuted(_ context.Context, e *ActionEvent[StateID, EventID, Context]) {
	if e.Data() != nil {
		e.Data().Count = 9999
	}
}

func TestObserverCannotMutateActorData(t *testing.T) {
	// Machine that increments Count by 1 on GO.
	m := New[StateID, EventID, Context]("ctx-safety").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("GO").
				Guard(func(_ Context) bool { return true }).
				Assign(func(c Context) Context { c.Count++; return c }).
				GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	rec := &RecordingObserver[StateID, EventID, Context]{}
	bar := newKindBarrier(KindTransition, 1)
	a := Start(m, Context{Count: 1},
		m.WithObservers(mutatingObserver{}, rec, bar),
	)
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	got := a.Data()
	if got.Count != 2 {
		t.Errorf("actor data Count = %d, want 2 (observer mutation must not leak through)", got.Count)
	}
}

// cloningContext implements Cloner so we can verify the Cloner path is used.
type cloningContext struct {
	Count   int
	cloned  *bool
	cloneMu *sync.Mutex
}

func (c cloningContext) Clone() cloningContext {
	c.cloneMu.Lock()
	*c.cloned = true
	c.cloneMu.Unlock()
	return c
}

type cState string
type cEvent string

func TestObserverUsesClonerWhenAvailable(t *testing.T) {
	cloned := false
	var mu sync.Mutex
	initial := cloningContext{Count: 1, cloned: &cloned, cloneMu: &mu}

	m := New[cState, cEvent, cloningContext]("cloner-ctx").
		Initial("a").
		State("a", func(s *StateBuilder[cState, cEvent, cloningContext]) {
			s.On("GO").GoTo("b")
		}).
		State("b", func(_ *StateBuilder[cState, cEvent, cloningContext]) {}).
		Build()

	rec := &RecordingObserver[cState, cEvent, cloningContext]{}
	barrier := make(chan struct{}, 1)
	signal := &cloneSignalObserver{ch: barrier}
	a := Start(m, initial, m.WithObservers(rec, signal))
	defer a.Stop()
	a.Send("GO")

	<-barrier // deterministic: blocks until OnTransition fires

	mu.Lock()
	defer mu.Unlock()
	if !cloned {
		t.Error("Cloner.Clone() was never invoked; defensive copy did not take the Cloner path")
	}
}

type cloneSignalObserver struct {
	BaseObserver[cState, cEvent, cloningContext]
	ch chan struct{}
}

func (c *cloneSignalObserver) OnTransition(context.Context, *TransitionEvent[cState, cEvent, cloningContext]) {
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

type raceContext struct {
	Count int
}

func (r *raceContext) Clone() *raceContext {
	if r == nil {
		return nil
	}
	return &raceContext{Count: r.Count}
}

func TestSnapshotRacesInvokeWrites(t *testing.T) {
	m := New[string, string, *raceContext]("race-machine").
		Initial("a").
		State("a", func(s *StateBuilder[string, string, *raceContext]) {
			s.Invoke(func(ctx context.Context, snap *raceContext, mutate func(func(*raceContext) *raceContext)) error {
				for {
					select {
					case <-ctx.Done():
						return nil
					default:
						mutate(func(c *raceContext) *raceContext {
							c.Count++
							return c
						})
					}
				}
			}, "b", "c")
		}).
		State("b", func(_ *StateBuilder[string, string, *raceContext]) {}).
		State("c", func(_ *StateBuilder[string, string, *raceContext]) {}).
		Build()

	a := Start(m, &raceContext{Count: 0})
	defer a.Stop()

	// Perform multiple Snapshots concurrently while the invoke loop is writing.
	// In the new implementation, because mutate updates under the actor's write lock,
	// and Snapshot acquires the read lock, there should be zero data races.
	for i := 0; i < 100; i++ {
		snap := a.Snapshot()
		if snap.Data == nil {
			t.Fatal("Snapshot returned a nil data")
		}
		if snap.Data.Count < 0 {
			t.Errorf("coherence check failed: snapshot Count is invalid: %d", snap.Data.Count)
		}
	}
}

func TestInvokeMutateAfterStateExit(t *testing.T) {
	mutateCalled := make(chan struct{})
	mutateDone := make(chan struct{})
	exited := make(chan struct{})

	m := New[StateID, EventID, Context]("test-exit").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, snap Context, mutate func(func(Context) Context)) error {
				close(mutateCalled)
				<-ctx.Done()
				close(exited)
				mutate(func(c Context) Context {
					c.Count = 9999
					return c
				})
				close(mutateDone)
				return nil
			}, "b", "b")
			s.On("GO").GoTo("b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{Count: 42})
	<-mutateCalled

	a.Send("GO")
	<-exited

	<-mutateDone

	got := a.Data()
	if got.Count == 9999 {
		t.Errorf("actor data was mutated after state exit: got %v, want 42", got.Count)
	}
	a.Stop()
}

func TestInvokeMutateAfterStop(t *testing.T) {
	mutateCalled := make(chan struct{})
	mutateDone := make(chan struct{})
	stopped := make(chan struct{})

	m := New[StateID, EventID, Context]("test-stop").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, snap Context, mutate func(func(Context) Context)) error {
				close(mutateCalled)
				<-ctx.Done()
				close(stopped)
				mutate(func(c Context) Context {
					c.Count = 9999
					return c
				})
				close(mutateDone)
				return nil
			}, "b", "b")
		}).
		State("b", func(_ *StateBuilder[StateID, EventID, Context]) {}).
		Build()

	a := Start(m, Context{Count: 42})
	<-mutateCalled

	a.Stop()
	<-stopped

	<-mutateDone

	got := a.Data()
	if got.Count == 9999 {
		t.Errorf("actor data was mutated after actor stop: got %v, want 42", got.Count)
	}
}

func TestInvokeReentryNewGeneration(t *testing.T) {
	mutate1Called := make(chan struct{})
	mutate1Done := make(chan struct{})
	enter2Done := make(chan struct{})

	var generation atomic.Int64

	m := New[StateID, EventID, Context]("test-reentry").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, snap Context, mutate func(func(Context) Context)) error {
				if generation.CompareAndSwap(0, 1) {
					close(mutate1Called)
					<-ctx.Done()
					mutate(func(c Context) Context {
						c.Count = 9999
						return c
					})
					close(mutate1Done)
				} else {
					generation.Store(2)
					close(enter2Done)
				}
				return nil
			}, "b", "b")
			s.On("GO").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("BACK").GoTo("a")
		}).
		Build()

	a := Start(m, Context{Count: 42})
	<-mutate1Called

	a.Send("GO")
	a.Send("BACK")
	<-enter2Done

	<-mutate1Done

	got := a.Data()
	if got.Count == 9999 {
		t.Errorf("actor data was mutated by obsolete generation: got %v, want 42", got.Count)
	}
	a.Stop()
}

type stabilityCtx struct {
	Count int
}

func (c stabilityCtx) Clone() stabilityCtx {
	return c
}

func TestInvokeSnapStability(t *testing.T) {
	done := make(chan struct{})
	m := New[string, string, stabilityCtx]("snap-stability").
		Initial("a").
		State("a", func(s *StateBuilder[string, string, stabilityCtx]) {
			s.Invoke(func(ctx context.Context, snap stabilityCtx, mutate func(func(stabilityCtx) stabilityCtx)) error {
				initialSnap := snap.Count
				mutate(func(c stabilityCtx) stabilityCtx {
					c.Count = 999
					return c
				})
				if snap.Count != initialSnap {
					t.Errorf("snap was aliased to live data: got %d, want %d", snap.Count, initialSnap)
				}
				close(done)
				return nil
			}, "b", "c")
		}).
		State("b", func(_ *StateBuilder[string, string, stabilityCtx]) {}).
		State("c", func(_ *StateBuilder[string, string, stabilityCtx]) {}).
		Build()

	a := Start(m, stabilityCtx{Count: 42})
	defer a.Stop()
	<-done
}

// TestInvokeParallelConcurrentMutate validates that Start's setup is locked against spawned
// invoke goroutines' mutate calls.
func TestInvokeParallelConcurrentMutate(t *testing.T) {
	done1 := make(chan struct{})
	done2 := make(chan struct{})

	m := New[string, string, stabilityCtx]("parallel-concurrent").
		Initial("par").
		State("par", func(s *StateBuilder[string, string, stabilityCtx]) {
			s.Type(Parallel)

			s.State("r1", func(s *StateBuilder[string, string, stabilityCtx]) {
				s.Initial("r1-active")
				s.State("r1-active", func(s *StateBuilder[string, string, stabilityCtx]) {
					s.Invoke(func(ctx context.Context, snap stabilityCtx, mutate func(func(stabilityCtx) stabilityCtx)) error {
						for i := 0; i < 50; i++ {
							mutate(func(c stabilityCtx) stabilityCtx {
								c.Count++
								return c
							})
						}
						close(done1)
						return nil
					}, "r1-done", "r1-done")
				})
				s.State("r1-done", func(_ *StateBuilder[string, string, stabilityCtx]) {})
			})

			s.State("r2", func(s *StateBuilder[string, string, stabilityCtx]) {
				s.Initial("r2-active")
				s.State("r2-active", func(s *StateBuilder[string, string, stabilityCtx]) {
					s.Invoke(func(ctx context.Context, snap stabilityCtx, mutate func(func(stabilityCtx) stabilityCtx)) error {
						for i := 0; i < 50; i++ {
							mutate(func(c stabilityCtx) stabilityCtx {
								c.Count++
								return c
							})
						}
						close(done2)
						return nil
					}, "r2-done", "r2-done")
				})
				s.State("r2-done", func(_ *StateBuilder[string, string, stabilityCtx]) {})
			})
		}).
		Build()

	a := Start(m, stabilityCtx{Count: 0})
	defer a.Stop()

	<-done1
	<-done2

	got := a.Data()
	if got.Count != 100 {
		t.Errorf("actor Data Count = %d, want 100 (concurrent writes must serialize correctly)", got.Count)
	}
}

func TestJSONWireFormat(t *testing.T) {
	// 1. Serialization uses the new "data" key
	snap := Snapshot[StateID, Context]{
		Active:  []StateID{"a"},
		History: map[StateID]StateID{},
		Data:    Context{Count: 42},
		ActorID: "my-actor",
	}
	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"data":{"Count":42}`) {
		t.Errorf("expected new json tag 'data' in serialized output, got %s", s)
	}
	if strings.Contains(s, `"context"`) {
		t.Errorf("expected no 'context' key in serialized output, got %s", s)
	}

	// 2. Deserializing new format works
	var snap2 Snapshot[StateID, Context]
	if err := json.Unmarshal(b, &snap2); err != nil {
		t.Fatalf("json.Unmarshal new: %v", err)
	}
	if snap2.Data.Count != 42 {
		t.Errorf("expected snap2.Data.Count to be 42, got %d", snap2.Data.Count)
	}

	// 3. Silent-Zero policy: Deserializing old format ("context" key) yields zero-value Data
	oldJSON := `{"active":["a"],"history":{},"context":{"Count":42},"actor_id":"my-actor"}`
	var snap3 Snapshot[StateID, Context]
	if err := json.Unmarshal([]byte(oldJSON), &snap3); err != nil {
		t.Fatalf("json.Unmarshal old: %v", err)
	}
	if snap3.Data.Count != 0 {
		t.Errorf("Silent-Zero policy failed: expected snap3.Data.Count to be zero (ignored/unpopulated), got %d", snap3.Data.Count)
	}
}

func TestInvokeMutateAfterInvokeCompleted(t *testing.T) {
	var savedMutate func(func(Context) Context)
	invokeDone := make(chan struct{})

	m := New[StateID, EventID, Context]("mutate-after-complete").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			// Targetless invoke: state "a" remains active after Invoke.Func returns!
			s.Invoke(func(_ context.Context, _ Context, mutate func(func(Context) Context)) error {
				savedMutate = mutate
				mutate(func(c Context) Context {
					c.Count = 10
					return c
				})
				return nil
			}, "", "")
		}).
		Build()

	obs := ObserverFuncs[StateID, EventID, Context]{
		InvokeCompletedFunc: func(context.Context, *InvokeEvent[StateID, EventID, Context]) {
			close(invokeDone)
		},
	}
	a := Start(m, Context{Count: 0}, m.WithObservers(obs))
	defer a.Stop()

	<-invokeDone
	if got := a.Data().Count; got != 10 {
		t.Fatalf("Data().Count after invoke = %d, want 10", got)
	}

	// Attempt to mutate using the captured closure after the invoke has completed
	// while state "a" is still active. Must be ignored!
	savedMutate(func(c Context) Context {
		c.Count = 9999
		return c
	})

	if got := a.Data().Count; got != 10 {
		t.Errorf("completed invoke mutate modified live data: got %d, want 10", got)
	}
}

func TestStaleServiceTransitionsIgnoredOnReentryAndStop(t *testing.T) {
	t.Run("stale_invoke_completion_ignored_after_reentry", func(t *testing.T) {
		invoke1Started := make(chan struct{})
		releaseInvoke1 := make(chan struct{})
		invoke1Completed := make(chan struct{})
		invoke2Started := make(chan struct{})

		var callCount atomic.Int32
		m := New[StateID, EventID, Context]("stale-invoke-reentry").
			Initial("a").
			State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
					if callCount.Add(1) == 1 {
						close(invoke1Started)
						<-releaseInvoke1
						// Return nil even though state "a" exited and re-entered!
						return nil
					}
					close(invoke2Started)
					<-ctx.Done()
					return ctx.Err()
				}, "done", "err")
				s.On("CYCLE").GoTo("b")
			}).
			State("b", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Always().GoTo("a")
			}).
			State("done", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			State("err", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		obs := ObserverFuncs[StateID, EventID, Context]{
			InvokeCompletedFunc: func(_ context.Context, _ *InvokeEvent[StateID, EventID, Context]) {
				select {
				case <-invoke1Completed:
				default:
					close(invoke1Completed)
				}
			},
		}
		a := Start(m, Context{}, m.WithObservers(obs))
		defer a.Stop()

		<-invoke1Started
		// Cycle a -> b -> a so "a" is active in generation 2 while invoke 1 is still finishing.
		a.Send("CYCLE")
		<-invoke2Started

		close(releaseInvoke1)
		<-invoke1Completed

		if got := a.State(); got != "a" {
			t.Errorf("stale invoke 1 transitioned actor to %q, want \"a\"", got)
		}
	})

	t.Run("stale_internal_transition_ignored_after_reentry_and_stop", func(t *testing.T) {
		m := New[StateID, EventID, Context]("stale-internal-trigger").
			Initial("a").
			State("a", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("CYCLE").GoTo("b")
			}).
			State("b", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Always().GoTo("a")
			}).
			State("c", func(_ *StateBuilder[StateID, EventID, Context]) {}).
			Build()

		done := make(chan struct{})
		a := Start(m, Context{Count: 1}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{
			TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
				if e.From == "b" && e.To == "a" {
					close(done)
				}
			},
		}))
		defer a.Stop()

		a.mu.RLock()
		gen1 := a.stateGens["a"]
		a.mu.RUnlock()

		// Cycle a -> b -> a so "a" is active again under a new entry generation.
		a.Send("CYCLE")
		<-done

		tr := &TransitionDef[StateID, EventID, Context]{
			Target: "c",
			Action: func(c Context) Context { c.Count = 999; return c },
		}

		// Firing an internal transition with the stale gen1 token must be ignored even though "a" is active.
		a.executeInternalTransition(context.Background(), "a", gen1, tr)
		if got := a.State(); got != "a" {
			t.Errorf("stale generation trigger moved state to %q, want \"a\"", got)
		}
		if got := a.Data().Count; got != 1 {
			t.Errorf("stale generation trigger ran action: Count=%d, want 1", got)
		}

		// Capture current generation and Stop the actor; firing with current generation after Stop must also no-op.
		a.mu.RLock()
		gen2 := a.stateGens["a"]
		a.mu.RUnlock()
		a.Stop()

		a.executeInternalTransition(context.Background(), "a", gen2, tr)
		if got := a.State(); got != "a" {
			t.Errorf("post-Stop internal trigger moved state to %q, want \"a\"", got)
		}
		if got := a.Data().Count; got != 1 {
			t.Errorf("post-Stop internal trigger ran action: Count=%d, want 1", got)
		}
	})
}
