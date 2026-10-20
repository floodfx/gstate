package gstate

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"testing"
)

type historicalData struct {
	Values []int
	Labels map[string]int
}

func (d *historicalData) Clone() *historicalData {
	return &historicalData{slices.Clone(d.Values), maps.Clone(d.Labels)}
}

func TestObserverHistoricalData(t *testing.T) {
	type B = StateBuilder[string, string, *historicalData]
	m := New[string, string, *historicalData]("history").Initial("a").State("a", func(s *B) {
		s.On("GO").Guard(func(*historicalData) bool { return true }).Assign(func(d *historicalData) *historicalData { d.Values[0]++; d.Labels["count"]++; return d }).GoTo("a")
	}).Build()
	var payloads []any
	var reads []func() *historicalData
	done := make(chan struct{}, 2)
	obs := ObserverFuncs[string, string, *historicalData]{
		TransitionFunc: func(_ context.Context, e *TransitionEvent[string, string, *historicalData]) {
			payloads = append(payloads, e)
			reads = append(reads, func() *historicalData { return *e.Data() })
			done <- struct{}{}
		},
		GuardEvaluatedFunc: func(_ context.Context, e *GuardEvent[string, string, *historicalData]) {
			payloads = append(payloads, e)
			reads = append(reads, func() *historicalData { return *e.Data() })
		},
		StateEnteredFunc: func(_ context.Context, e *StateEvent[string, string, *historicalData]) {
			payloads = append(payloads, e)
			reads = append(reads, func() *historicalData { return *e.Data() })
		},
		StateExitedFunc: func(_ context.Context, e *StateEvent[string, string, *historicalData]) {
			payloads = append(payloads, e)
			reads = append(reads, func() *historicalData { return *e.Data() })
		},
		ActionExecutedFunc: func(_ context.Context, e *ActionEvent[string, string, *historicalData]) {
			payloads = append(payloads, e)
			reads = append(reads, func() *historicalData { return *e.Data() })
		},
	}
	a := Start(m, &historicalData{[]int{0}, map[string]int{"count": 0}}, m.WithObservers(obs))
	defer a.Stop()
	a.Send("GO")
	<-done
	// Read State() to acquire the lock after the entire first microstep finishes.
	_ = a.State()
	firstPayloads := slices.Clone(payloads)
	firstReads := slices.Clone(reads)
	a.Send("GO")
	<-done
	_ = a.State()
	want := []int{0, 0, 0, 1, 1, 1} // initial entry, guard, exit, action, entry, transition
	if len(firstReads) != len(want) {
		t.Fatalf("payload count=%d", len(firstReads))
	}
	for i, read := range firstReads {
		d := read()
		if d.Values[0] != want[i] || d.Labels["count"] != want[i] {
			t.Errorf("payload %d data=%+v, want %d", i, d, want[i])
		}
		raw, err := json.Marshal(firstPayloads[i])
		if err != nil {
			t.Fatal(err)
		}
		var result struct{ Data *historicalData }
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.Values[0] != want[i] {
			t.Errorf("deferred JSON %d=%s", i, raw)
		}
	}
}

func TestDeferredObserverJSONDuringInvokeMutation(t *testing.T) {
	mutateNow := make(chan struct{})
	mutated := make(chan struct{})
	var retained *StateEvent[string, string, *historicalData]
	m := New[string, string, *historicalData]("invoke-history").Initial("a").State("a", func(s *StateBuilder[string, string, *historicalData]) {
		s.Invoke(func(ctx context.Context, _ *historicalData, mutate func(func(*historicalData) *historicalData)) error {
			<-mutateNow
			for i := 0; i < 100; i++ {
				mutate(func(d *historicalData) *historicalData { d.Values[0]++; d.Labels["count"]++; return d })
			}
			close(mutated)
			<-ctx.Done()
			return nil
		}, "", "")
	}).Build()
	a := Start(m, &historicalData{[]int{0}, map[string]int{"count": 0}}, m.WithObservers(ObserverFuncs[string, string, *historicalData]{StateEnteredFunc: func(_ context.Context, e *StateEvent[string, string, *historicalData]) { retained = e }}))
	defer a.Stop()
	close(mutateNow)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				raw, err := json.Marshal(retained)
				if err != nil {
					t.Error(err)
					return
				}
				var v struct{ Data *historicalData }
				if err := json.Unmarshal(raw, &v); err != nil {
					t.Error(err)
					return
				}
				if v.Data.Values[0] != 0 || v.Data.Labels["count"] != 0 {
					t.Errorf("historical JSON changed: %s", raw)
					return
				}
			}
		})
	}
	<-mutated
	wg.Wait()
	if got := a.Data().Values[0]; got != 100 {
		t.Fatalf("live count=%d", got)
	}
}
