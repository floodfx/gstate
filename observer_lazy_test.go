package gstate

import (
	"context"
	"sync/atomic"
	"testing"
)

type countingCloner struct {
	Count      int
	cloneCount *atomic.Int64
}

func (c countingCloner) Clone() countingCloner {
	if c.cloneCount != nil {
		c.cloneCount.Add(1)
	}
	return c
}

type lState string
type lEvent string

type testTransitionBarrier struct {
	BaseObserver[lState, lEvent, countingCloner]
	done chan struct{}
}

func (b *testTransitionBarrier) OnTransition(ctx context.Context, e *TransitionEvent[lState, lEvent, countingCloner]) {
	close(b.done)
}

type noopTransitionObserver struct {
	BaseObserver[lState, lEvent, countingCloner]
}

func (noopTransitionObserver) OnTransition(ctx context.Context, e *TransitionEvent[lState, lEvent, countingCloner]) {
	// ignores e.Data()
}

type dataCallingObserver struct {
	BaseObserver[lState, lEvent, countingCloner]
	calls int
}

func (o *dataCallingObserver) OnTransition(ctx context.Context, e *TransitionEvent[lState, lEvent, countingCloner]) {
	for i := 0; i < o.calls; i++ {
		_ = e.Data()
	}
}

type dummyGuardObserver struct {
	BaseObserver[lState, lEvent, countingCloner]
}

func (dummyGuardObserver) OnGuardEvaluated(ctx context.Context, e *GuardEvent[lState, lEvent, countingCloner]) {
	_ = e.Data()
}

func createLazyTestMachine() *Machine[lState, lEvent, countingCloner] {
	return New[lState, lEvent, countingCloner]("lazy-machine").
		Initial("a").
		State("a", func(s *StateBuilder[lState, lEvent, countingCloner]) {
			s.On("GO").
				Guard(func(c countingCloner) bool { return true }).
				GoTo("b")
		}).
		State("b", func(_ *StateBuilder[lState, lEvent, countingCloner]) {}).
		Build()
}

func TestNoObserver_ZeroClones(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got := cloneCount.Load(); got != 0 {
		t.Errorf("expected 0 clones with no observer, got %d", got)
	}
}

func TestObserverNoDataCall_ZeroClones(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	obs := noopTransitionObserver{}
	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(obs, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got := cloneCount.Load(); got != 0 {
		t.Errorf("expected 0 clones when observer ignores Data, got %d", got)
	}
}

func TestObserverDataCallOnce_OneClone(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	obs := &dataCallingObserver{calls: 1}
	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(obs, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	if got := cloneCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 clone when observer calls Data() once, got %d", got)
	}
}

func TestObserverDataCallMultiple_OneClone(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	obs := &dataCallingObserver{calls: 3}
	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(obs, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	// Should be exactly 1 due to memoization
	if got := cloneCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 clone (memoized) when observer calls Data() multiple times, got %d", got)
	}
}

func TestMultipleObserversSharedDataCall_OneClone(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	obs1 := &dataCallingObserver{calls: 1}
	obs2 := &dataCallingObserver{calls: 2}
	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(obs1, obs2, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	// Both observers call Data() on the shared event pointer. Should result in exactly 1 clone.
	if got := cloneCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 clone (shared sync.Once) across multiple observers, got %d", got)
	}
}

func TestRecordingObserver_MaterializedData(t *testing.T) {
	var cloneCount atomic.Int64
	initial := countingCloner{Count: 42, cloneCount: &cloneCount}
	m := createLazyTestMachine()

	rec := &RecordingObserver[lState, lEvent, countingCloner]{}
	bar := &testTransitionBarrier{done: make(chan struct{})}
	a := Start(m, initial, m.WithObservers(rec, bar))
	defer a.Stop()

	a.Send("GO")
	<-bar.done

	// RecordingObserver materializes Data eagerly at record time.
	// Total clones expected: 5 (OnStateEntered x2, OnGuardEvaluated, OnStateExited, OnTransition).
	if got := cloneCount.Load(); got != 5 {
		t.Errorf("expected 5 clones for RecordingObserver, got %d", got)
	}

	transitions := rec.Transitions()
	if len(transitions) != 1 {
		t.Fatalf("expected 1 recorded transition, got %d", len(transitions))
	}
	if transitions[0].Data().Count != 42 {
		t.Errorf("expected recorded transition data to be 42, got %d", transitions[0].Data().Count)
	}
}

func TestWithObservers_Filtering(t *testing.T) {
	m := createLazyTestMachine()

	obs1 := &dataCallingObserver{calls: 1}
	obs2 := &dummyGuardObserver{}

	a := Start(m, countingCloner{Count: 42}, m.WithObservers(obs1, obs2))
	defer a.Stop()

	a.Send("GO")
}
