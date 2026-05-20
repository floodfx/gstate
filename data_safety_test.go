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
	NopObserver[StateID, EventID, Context]
}

func (mutatingObserver) OnStateEntered(_ context.Context, e StateEvent[StateID, EventID, Context]) {
	if e.Data != nil {
		e.Data.Count = 9999
	}
}
func (mutatingObserver) OnStateExited(_ context.Context, e StateEvent[StateID, EventID, Context]) {
	if e.Data != nil {
		e.Data.Count = 9999
	}
}
func (mutatingObserver) OnTransition(_ context.Context, e TransitionEvent[StateID, EventID, Context]) {
	if e.Data != nil {
		e.Data.Count = 9999
	}
}
func (mutatingObserver) OnGuardEvaluated(_ context.Context, e GuardEvent[StateID, EventID, Context]) {
	if e.Data != nil {
		e.Data.Count = 9999
	}
}
func (mutatingObserver) OnActionExecuted(_ context.Context, e ActionEvent[StateID, EventID, Context]) {
	if e.Data != nil {
		e.Data.Count = 9999
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
		m.WithObserver(
			MultiObserver[StateID, EventID, Context]{mutatingObserver{}, rec, bar},
		),
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
	a := Start(m, initial, m.WithObserver(MultiObserver[cState, cEvent, cloningContext]{rec, signal}))
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
	NopObserver[cState, cEvent, cloningContext]
	ch chan struct{}
}

func (c *cloneSignalObserver) OnTransition(context.Context, TransitionEvent[cState, cEvent, cloningContext]) {
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


