package gstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

type completionRuntimeState = StateBuilder[StateID, EventID, Context]

func completionWait(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("completion synchronization timed out")
	}
}

func TestCompletionRuntimeExitAssignOrder(t *testing.T) {
	m := New[StateID, EventID, Context]("completion-actions").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("final")
		s.Entry(func(d Context) Context { d.Count = 1; return d })
		s.Exit(func(d Context) Context { d.Count *= 10; return d })
		s.State("final", func(f *completionRuntimeState) { f.Type(Final) })
		s.OnDone().Guard(func(d Context) bool { return d.Count == 1 }).Assign(func(d Context) Context { d.Count++; return d }).GoTo("accepted")
		s.OnDone().GoTo("rejected")
	}).State("accepted", func(s *completionRuntimeState) {}).State("rejected", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if a.State() != "accepted" || a.Snapshot().Data.Count != 11 {
		t.Fatalf("state=%s data=%+v; guard must precede Exit, Assign must follow Exit", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeAlwaysPrecedesDone(t *testing.T) {
	m := New[StateID, EventID, Context]("completion-priority").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("final")
		s.State("final", func(f *completionRuntimeState) { f.Type(Final) })
		s.Always().GoTo("eventless")
		s.OnDone().GoTo("completed")
	}).State("eventless", func(s *completionRuntimeState) {}).State("completed", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if a.State() != "eventless" {
		t.Fatalf("state=%s, wanted eventless priority", a.State())
	}
}

func TestCompletionRuntimeInvokeMutationSettlesBeforeReturning(t *testing.T) {
	returned := make(chan struct{})
	m := New[StateID, EventID, Context]("completion-mutate").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("running")
		s.State("running", func(r *completionRuntimeState) {
			r.Invoke(func(ctx context.Context, d Context, mutate func(func(Context) Context)) error {
				mutate(func(d Context) Context { d.Count = 1; return d })
				close(returned)
				return nil
			}, "", "")
			r.Always().Guard(func(d Context) bool { return d.Count == 1 }).GoTo("final")
		})
		s.State("final", func(f *completionRuntimeState) { f.Type(Final) })
		s.OnDone().Assign(func(d Context) Context { d.Count = 2; return d }).GoTo("ready")
	}).State("ready", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	completionWait(t, returned)
	if a.State() != "ready" || a.Snapshot().Data.Count != 2 {
		t.Fatalf("mutation returned before completion: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeParallelEventBatch(t *testing.T) {
	for _, reopen := range []bool{false, true} {
		t.Run(map[bool]string{false: "both_finish", true: "finish_and_reopen"}[reopen], func(t *testing.T) {
			settled := make(chan struct{}, 4)
			m := New[StateID, EventID, Context]("completion-batch").Initial("parallel").State("parallel", func(p *completionRuntimeState) {
				p.Type(Parallel)
				p.State("a", func(r *completionRuntimeState) {
					r.Initial("a_work")
					r.State("a_work", func(s *completionRuntimeState) { s.On("STEP").GoTo("a_final") })
					r.State("a_final", func(s *completionRuntimeState) { s.Type(Final) })
				})
				p.State("b", func(r *completionRuntimeState) {
					if reopen {
						r.Initial("b_final")
					} else {
						r.Initial("b_work")
					}
					r.On("STEP").Guard(func(d Context) bool { return reopen }).GoTo("b_work")
					r.State("b_work", func(s *completionRuntimeState) {
						s.On("STEP").Guard(func(d Context) bool { return !reopen }).GoTo("b_final")
					})
					r.State("b_final", func(s *completionRuntimeState) { s.Type(Final) })
				})
				p.OnDone().Assign(func(d Context) Context { d.Count++; return d }).GoTo("ready")
			}).State("ready", func(s *completionRuntimeState) {}).Build()
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
				if e.Event == "STEP" {
					settled <- struct{}{}
				}
			}}))
			defer a.Stop()
			a.Send("STEP")
			completionWait(t, settled)
			completionWait(t, settled)
			snap := a.Snapshot()
			if reopen {
				if snap.Data.Count != 0 || a.State() == "ready" {
					t.Fatalf("mid-batch completion fired: %+v", snap)
				}
			} else if a.State() != "ready" || snap.Data.Count != 1 {
				t.Fatalf("completion missing or duplicated: %+v", snap)
			}
		})
	}
}

func TestCompletionRuntimeParallelWaitsAcrossEvents(t *testing.T) {
	step := make(chan struct{}, 8)
	m := New[StateID, EventID, Context]("completion-wait").Initial("parallel").State("parallel", func(p *completionRuntimeState) {
		p.Type(Parallel)
		for _, id := range []StateID{"a", "b"} {
			p.State(id, func(r *completionRuntimeState) {
				work, final := id+"_work", id+"_final"
				r.Initial(work)
				r.State(work, func(s *completionRuntimeState) { s.On(EventID(id)).GoTo(final) })
				r.State(final, func(s *completionRuntimeState) { s.Type(Final) })
			})
		}
		p.On("PING").Assign(func(d Context) Context { d.Count++; return d })
		p.OnDone().Assign(func(d Context) Context { d.Count += 10; return d }).GoTo("ready")
	}).State("ready", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
		if e.Event != "" {
			step <- struct{}{}
		}
	}}))
	defer a.Stop()
	for _, event := range []EventID{"a", "PING", "b"} {
		a.Send(event)
		completionWait(t, step)
		_ = a.Snapshot()
	}
	if a.State() != "ready" || a.Snapshot().Data.Count != 11 {
		t.Fatalf("waiting region lost completion: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeHydrateSeedsFinalWithoutEntry(t *testing.T) {
	m := New[StateID, EventID, Context]("completion-hydrate").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("running")
		s.State("running", func(s *completionRuntimeState) {})
		s.State("final", func(s *completionRuntimeState) {
			s.Type(Final)
			s.Entry(func(d Context) Context { d.Count += 100; return d })
		})
		s.OnDone().Assign(func(d Context) Context { d.Count++; return d }).GoTo("ready")
	}).State("ready", func(s *completionRuntimeState) {}).Build()
	a := Hydrate(m, Snapshot[StateID, Context]{Active: []StateID{"work", "final"}, Data: Context{Count: 5}})
	defer a.Stop()
	if a.State() != "ready" || a.Snapshot().Data.Count != 6 {
		t.Fatalf("hydration did not normalize once without entry: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeSharedTransitionBudget(t *testing.T) {
	m := New[StateID, EventID, Context]("completion-loop").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("final")
		s.State("final", func(s *completionRuntimeState) { s.Type(Final) })
		s.OnDone().GoTo("again")
	}).State("again", func(s *completionRuntimeState) { s.Always().GoTo("work") }).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if !errors.Is(a.Err(), ErrAlwaysLimit) {
		t.Fatalf("unbounded completion/eventless loop: %v", a.Err())
	}
}

func TestCompletionRuntimeNestedParallelChildHandlerPreemptsParent(t *testing.T) {
	m := New[StateID, EventID, Context]("nested-parallel").Initial("outer").State("outer", func(p *completionRuntimeState) {
		p.Type(Parallel)
		p.State("inner", func(i *completionRuntimeState) {
			i.Type(Parallel)
			for _, id := range []StateID{"a", "b"} {
				i.State(id, func(r *completionRuntimeState) {
					r.Initial(id + "_final")
					r.State(id+"_final", func(s *completionRuntimeState) { s.Type(Final) })
				})
			}
			i.OnDone().Assign(func(d Context) Context { d.Count = 1; return d }).GoTo("child_won")
		})
		p.State("other", func(r *completionRuntimeState) {
			r.Initial("other_final")
			r.State("other_final", func(s *completionRuntimeState) { s.Type(Final) })
		})
		p.OnDone().Assign(func(d Context) Context { d.Count = 10; return d }).GoTo("parent_won")
	}).State("child_won", func(s *completionRuntimeState) {}).State("parent_won", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	if a.State() != "child_won" || a.Snapshot().Data.Count != 1 {
		t.Fatalf("nested completion ordering: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeHistoryReentryCompletesNewActivation(t *testing.T) {
	step := make(chan struct{}, 4)
	m := New[StateID, EventID, Context]("completion-history").Initial("work").State("work", func(s *completionRuntimeState) {
		s.History(Deep)
		s.Initial("running")
		s.State("running", func(s *completionRuntimeState) { s.On("FINISH").GoTo("final") })
		s.State("final", func(s *completionRuntimeState) {
			s.Type(Final)
			s.Entry(func(d Context) Context { d.Count++; return d })
		})
		s.OnDone().GoTo("away")
	}).State("away", func(s *completionRuntimeState) { s.On("BACK").GoTo("work") }).Build()
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
		if e.Event != "" {
			step <- struct{}{}
		}
	}}))
	defer a.Stop()
	a.Send("FINISH")
	completionWait(t, step)
	if a.State() != "away" || a.Snapshot().Data.Count != 1 {
		t.Fatal("first activation did not finish")
	}
	a.Send("BACK")
	completionWait(t, step)
	if a.State() != "away" || a.Snapshot().Data.Count != 2 {
		t.Fatalf("history final entry did not complete new activation: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimeCancelledInvokeCannotComplete(t *testing.T) {
	started, cancelled, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	m := New[StateID, EventID, Context]("completion-cancel").Initial("work").State("work", func(s *completionRuntimeState) {
		s.Initial("running")
		s.On("CANCEL").GoTo("idle")
		s.State("running", func(s *completionRuntimeState) {
			s.Invoke(func(ctx context.Context, d Context, mutate func(func(Context) Context)) error {
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-release
				mutate(func(d Context) Context { d.Count = 1; return d })
				close(returned)
				return nil
			}, "", "")
			s.Always().Guard(func(d Context) bool { return d.Count == 1 }).GoTo("final")
		})
		s.State("final", func(s *completionRuntimeState) { s.Type(Final) })
		s.OnDone().GoTo("ready")
	}).State("idle", func(s *completionRuntimeState) {}).State("ready", func(s *completionRuntimeState) {}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	completionWait(t, started)
	a.Send("CANCEL")
	completionWait(t, cancelled)
	close(release)
	completionWait(t, returned)
	if a.State() != "idle" || a.Snapshot().Data.Count != 0 {
		t.Fatalf("cancelled invocation completed: %s %+v", a.State(), a.Snapshot().Data)
	}
}

func TestCompletionRuntimePeerOrderAndParallelPropagation(t *testing.T) {
	m := New[StateID, EventID, Context]("completion-peer-order").Initial("parallel").State("parallel", func(p *completionRuntimeState) {
		p.Type(Parallel)
		// Reverse declaration order; peers must still settle in state-ID order.
		for _, id := range []StateID{"b", "a"} {
			p.State(id, func(r *completionRuntimeState) {
				flow, work, done, end := id+"_flow", id+"_work", id+"_done", id+"_end"
				digit := 1
				if id == "b" {
					digit = 2
				}
				r.Initial(flow)
				r.State(flow, func(s *completionRuntimeState) {
					s.Initial(work)
					s.State(work, func(s *completionRuntimeState) { s.On("FINISH").GoTo(done) })
					s.State(done, func(s *completionRuntimeState) { s.Type(Final) })
					s.OnDone().Assign(func(d Context) Context { d.Count = d.Count*10 + digit; return d }).GoTo(end)
				})
				r.State(end, func(s *completionRuntimeState) { s.Type(Final) })
			})
		}
		p.OnDone().Assign(func(d Context) Context { d.Count = d.Count*10 + 3; return d }).GoTo("ready")
	}).State("ready", func(s *completionRuntimeState) {}).Build()
	completed := make(chan struct{})
	a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{StateEnteredFunc: func(_ context.Context, e *StateEvent[StateID, EventID, Context]) {
		if e.State == "ready" {
			close(completed)
		}
	}}))
	defer a.Stop()
	a.Send("FINISH")
	completionWait(t, completed)
	if got := a.Snapshot().Data.Count; got != 123 {
		t.Fatalf("peer/ancestor completion order = %d, want 123", got)
	}
}

func TestCompletionRuntimeDirectFinalParallelRegion(t *testing.T) {
	for _, initiallyDone := range []bool{false, true} {
		t.Run(map[bool]string{false: "finishes later", true: "initially complete"}[initiallyDone], func(t *testing.T) {
			reached := make(chan struct{})
			m := New[StateID, EventID, Context]("direct-final-region").Initial("parallel").State("parallel", func(p *completionRuntimeState) {
				p.Type(Parallel)
				p.State("direct_final", func(f *completionRuntimeState) { f.Type(Final) })
				p.State("region", func(r *completionRuntimeState) {
					if initiallyDone {
						r.Initial("region_final")
					} else {
						r.Initial("working")
					}
					r.State("working", func(s *completionRuntimeState) { s.On("FINISH").GoTo("region_final") })
					r.State("region_final", func(f *completionRuntimeState) { f.Type(Final) })
				})
				p.OnDone().GoTo("completed")
			}).State("completed", func(s *completionRuntimeState) {}).Build()
			a := Start(m, Context{}, m.WithObservers(ObserverFuncs[StateID, EventID, Context]{StateEnteredFunc: func(_ context.Context, e *StateEvent[StateID, EventID, Context]) {
				if e.State == "completed" {
					close(reached)
				}
			}}))
			defer a.Stop()
			if !initiallyDone {
				if !a.Matches("working") {
					t.Fatal("parallel completed before its remaining region")
				}
				a.Send("FINISH")
			}
			completionWait(t, reached)
			if !a.Matches("completed") {
				t.Fatalf("states: %v", a.States())
			}
		})
	}
}

func TestCompletionQueueDeduplicatesPendingActivation(t *testing.T) {
	m := New[StateID, EventID, Context]("pending").Initial("parallel").State("parallel", func(p *completionRuntimeState) {
		p.Type(Parallel)
		p.State("final", func(f *completionRuntimeState) { f.Type(Final) })
	}).Build()
	for _, pendingGeneration := range []uint64{1, 2} {
		// Construct the locked scheduler's configuration with a completion still
		// pending while a later eventless step enters the same final region.
		a := &Actor[StateID, EventID, Context]{machine: m, active: map[StateID]bool{"parallel": true, "final": true}, stateGens: map[StateID]uint64{"parallel": 2, "final": 3}, pendingCompletions: []completionOccurrence[StateID]{{"parallel", pendingGeneration}}, pendingFinalEntries: []completionOccurrence[StateID]{{"final", 3}}}
		a.flushFinalEntries()
		want := 1
		if pendingGeneration != 2 {
			want = 2
		}
		if len(a.pendingCompletions) != want {
			t.Fatalf("pending generation %d: queue = %v, want %d occurrences", pendingGeneration, a.pendingCompletions, want)
		}
	}
}
