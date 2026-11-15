package gstate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type errData struct {
	Count int
	Err   string
	Trail []string
}

func (d errData) Clone() errData {
	if d.Trail != nil {
		trail := make([]string, len(d.Trail))
		copy(trail, d.Trail)
		d.Trail = trail
	}
	return d
}

func recordGuardErr(d errData, err error) errData {
	d.Err = err.Error()
	return d
}

func appendErrTrail(step string) func(errData) errData {
	return func(d errData) errData {
		d.Trail = append(d.Trail, step)
		return d
	}
}

func newErrTransitionBarrier(n int) (Observer[StateID, EventID, errData], <-chan struct{}) {
	done := make(chan struct{})
	var mu sync.Mutex
	seen := 0
	obs := ObserverFuncs[StateID, EventID, errData]{
		TransitionFunc: func(context.Context, *TransitionEvent[StateID, EventID, errData]) {
			mu.Lock()
			defer mu.Unlock()
			seen++
			if seen == n {
				close(done)
			}
		},
	}
	return obs, done
}

func waitErrSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for observer signal")
	}
}

func TestGuardErr_EventSingleState(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("guard boom")

	buildMachine := func(guardFn func(errData) (bool, error)) *Machine[StateID, EventID, errData] {
		return New[StateID, EventID, errData]("guard-err-single").
			Initial("idle").
			State("idle", func(s *SB) {
				s.Exit(appendErrTrail("exit"))
				s.On("GO").
					GuardErr(guardFn, "failed", recordGuardErr).
					Assign(appendErrTrail("assign")).
					GoTo("ok")
				s.On("GO").GoTo("fallback")
			}).
			State("ok", func(*SB) {}).
			State("fallback", func(*SB) {}).
			State("failed", func(*SB) {}).
			Build()
	}

	t.Run("error_routes_to_onError_and_skips_assign_and_fallback", func(t *testing.T) {
		m := buildMachine(func(errData) (bool, error) { return true, sentinel })
		bar, done := newErrTransitionBarrier(1)
		rec := &RecordingObserver[StateID, EventID, errData]{}
		a := Start(m, errData{}, m.WithObservers(rec, bar))
		defer a.Stop()

		a.SendWith("GO", func(d errData) errData {
			d.Count = 42
			return d
		})
		waitErrSignal(t, done)

		if got := a.State(); got != "failed" {
			t.Fatalf("State() = %q, want %q", got, "failed")
		}
		data := a.Data()
		if data.Err != sentinel.Error() {
			t.Fatalf("Data().Err = %q, want %q", data.Err, sentinel.Error())
		}
		if !slices.Equal(data.Trail, []string{"exit"}) {
			t.Fatalf("Data().Trail = %v, want [exit]", data.Trail)
		}
		if data.Count != 42 {
			t.Fatalf("SendWith payload was not committed on guard error: Count = %d, want 42", data.Count)
		}
		if dropped := rec.EventsDropped(); len(dropped) != 0 {
			t.Fatalf("expected 0 dropped events on guard error, got %v", dropped)
		}
	})

	t.Run("true_nil_fires_transition_and_runs_assign", func(t *testing.T) {
		m := buildMachine(func(errData) (bool, error) { return true, nil })
		bar, done := newErrTransitionBarrier(1)
		a := Start(m, errData{}, m.WithObservers(bar))
		defer a.Stop()

		a.Send("GO")
		waitErrSignal(t, done)

		if got := a.State(); got != "ok" {
			t.Fatalf("State() = %q, want %q", got, "ok")
		}
		data := a.Data()
		if data.Err != "" {
			t.Fatalf("Data().Err = %q, want empty", data.Err)
		}
		if !slices.Equal(data.Trail, []string{"exit", "assign"}) {
			t.Fatalf("Data().Trail = %v, want [exit assign]", data.Trail)
		}
	})

	t.Run("false_nil_falls_through_to_next_candidate", func(t *testing.T) {
		m := buildMachine(func(errData) (bool, error) { return false, nil })
		bar, done := newErrTransitionBarrier(1)
		a := Start(m, errData{}, m.WithObservers(bar))
		defer a.Stop()

		a.Send("GO")
		waitErrSignal(t, done)

		if got := a.State(); got != "fallback" {
			t.Fatalf("State() = %q, want %q", got, "fallback")
		}
		data := a.Data()
		if data.Err != "" {
			t.Fatalf("Data().Err = %q, want empty", data.Err)
		}
		if !slices.Equal(data.Trail, []string{"exit"}) {
			t.Fatalf("Data().Trail = %v, want [exit]", data.Trail)
		}
	})

	t.Run("exit_record_entry_ordering_and_nil_record", func(t *testing.T) {
		m := New[StateID, EventID, errData]("guard-err-order").
			Initial("idle").
			State("idle", func(s *SB) {
				s.Exit(appendErrTrail("exit"))
				s.On("GO").
					GuardErr(func(errData) (bool, error) { return false, sentinel }, "step2", func(d errData, err error) errData {
						d.Err = err.Error()
						d.Trail = append(d.Trail, "record")
						return d
					}).
					GoTo("ok")
			}).
			State("step2", func(s *SB) {
				s.Entry(appendErrTrail("entry"))
				s.On("NEXT").
					GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", nil).
					GoTo("ok")
			}).
			State("ok", func(*SB) {}).
			State("failed", func(*SB) {}).
			Build()

		bar, done := newErrTransitionBarrier(2)
		a := Start(m, errData{}, m.WithObservers(bar))
		defer a.Stop()

		a.Send("GO")
		a.Send("NEXT")
		waitErrSignal(t, done)

		if got := a.State(); got != "failed" {
			t.Fatalf("State() = %q, want %q", got, "failed")
		}
		if got := a.Data().Trail; !slices.Equal(got, []string{"exit", "record", "entry"}) {
			t.Fatalf("Trail = %v, want [exit record entry]", got)
		}
	})
}

func TestGuardErr_ParallelErrorPreemptsOtherRegions(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("region b guard failed")
	regionCChecked := false

	m := New[StateID, EventID, errData]("guard-err-parallel").
		Initial("p").
		State("p", func(p *SB) {
			p.Type(Parallel)
			p.State("a", func(r *SB) {
				r.Initial("a1")
				r.State("a1", func(s *SB) {
					s.On("GO").Assign(appendErrTrail("a_assign")).GoTo("a2")
				})
				r.State("a2", func(s *SB) { s.Type(Final) })
			})
			p.State("b", func(r *SB) {
				r.Initial("b1")
				r.State("b1", func(s *SB) {
					s.On("GO").
						GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", recordGuardErr).
						GoTo("b2")
				})
				r.State("b2", func(s *SB) { s.Type(Final) })
			})
			p.State("c", func(r *SB) {
				r.Initial("c1")
				r.State("c1", func(s *SB) {
					s.On("GO").Guard(func(errData) bool {
						regionCChecked = true
						return true
					}).GoTo("c2")
				})
				r.State("c2", func(s *SB) { s.Type(Final) })
			})
			p.OnDone().GoTo("success")
		}).
		State("success", func(s *SB) {
			s.Entry(appendErrTrail("success"))
		}).
		State("failed", func(*SB) {}).
		Build()

	bar, done := newErrTransitionBarrier(1)
	a := Start(m, errData{}, m.WithObservers(bar))
	defer a.Stop()

	a.Send("GO")
	waitErrSignal(t, done)

	if got := a.State(); got != "failed" {
		t.Fatalf("State() = %q, want %q", got, "failed")
	}
	data := a.Data()
	if data.Err != sentinel.Error() {
		t.Fatalf("Data().Err = %q, want %q", data.Err, sentinel.Error())
	}
	if len(data.Trail) != 0 {
		t.Fatalf("expected neither region a Assign nor success Entry to run, got Trail=%v", data.Trail)
	}
	if regionCChecked {
		t.Fatal("expected guard selection to stop immediately on error without evaluating later regions")
	}
}

func TestGuardErr_AlwaysChain(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("always guard boom")

	t.Run("stops_chain_and_routes_to_error", func(t *testing.T) {
		m := New[StateID, EventID, errData]("guard-err-always").
			Initial("idle").
			State("idle", func(s *SB) {
				s.On("GO").GoTo("check1")
			}).
			State("check1", func(s *SB) {
				s.Always().GoTo("check2")
			}).
			State("check2", func(s *SB) {
				s.Always().
					GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", recordGuardErr).
					GoTo("ok")
				s.Always().GoTo("fallback")
			}).
			State("ok", func(*SB) {}).
			State("fallback", func(s *SB) {
				s.Entry(appendErrTrail("fallback"))
			}).
			State("failed", func(*SB) {}).
			Build()

		bar, done := newErrTransitionBarrier(3)
		a := Start(m, errData{}, m.WithObservers(bar))
		defer a.Stop()

		a.Send("GO")
		waitErrSignal(t, done)

		if got := a.State(); got != "failed" {
			t.Fatalf("State() = %q, want %q", got, "failed")
		}
		data := a.Data()
		if data.Err != sentinel.Error() {
			t.Fatalf("Data().Err = %q, want %q", data.Err, sentinel.Error())
		}
		if len(data.Trail) != 0 {
			t.Fatalf("fallback should not be entered, got Trail=%v", data.Trail)
		}
	})

	t.Run("error_loop_exhausts_always_limit", func(t *testing.T) {
		m := New[StateID, EventID, errData]("guard-err-always-limit").
			Initial("idle").
			State("idle", func(s *SB) {
				s.On("GO").GoTo("loopA")
			}).
			State("loopA", func(s *SB) {
				s.Always().
					GuardErr(func(errData) (bool, error) { return false, sentinel }, "loopB", recordGuardErr).
					GoTo("ok")
			}).
			State("loopB", func(s *SB) {
				s.Always().
					GuardErr(func(errData) (bool, error) { return false, sentinel }, "loopA", recordGuardErr).
					GoTo("ok")
			}).
			State("ok", func(*SB) {}).
			Build()

		a := Start(m, errData{})
		defer a.Stop()

		a.Send("GO")
		waitErrSignal(t, a.Done())

		if !errors.Is(a.Err(), ErrAlwaysLimit) {
			t.Fatalf("Err() = %v, want %v", a.Err(), ErrAlwaysLimit)
		}
	})
}

func TestGuardErr_OnDone(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("ondone guard boom")

	m := New[StateID, EventID, errData]("guard-err-ondone").
		Initial("work").
		State("work", func(w *SB) {
			w.Initial("running")
			w.State("running", func(s *SB) {
				s.On("GO").GoTo("finished")
			})
			w.State("finished", func(s *SB) {
				s.Type(Final)
			})
			w.OnDone().
				GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", recordGuardErr).
				GoTo("next")
			w.OnDone().GoTo("success")
		}).
		State("next", func(*SB) {}).
		State("success", func(s *SB) {
			s.Entry(appendErrTrail("success"))
		}).
		State("failed", func(*SB) {}).
		Build()

	bar, done := newErrTransitionBarrier(2)
	a := Start(m, errData{}, m.WithObservers(bar))
	defer a.Stop()

	a.Send("GO")
	waitErrSignal(t, done)

	if got := a.State(); got != "failed" {
		t.Fatalf("State() = %q, want %q", got, "failed")
	}
	data := a.Data()
	if data.Err != sentinel.Error() {
		t.Fatalf("Data().Err = %q, want %q", data.Err, sentinel.Error())
	}
	if len(data.Trail) != 0 {
		t.Fatalf("success fallback should not run, got Trail=%v", data.Trail)
	}
}

func TestGuardErr_After(t *testing.T) {
	// A real timer duration is the behavior under test; the transition callback
	// deterministically signals completion.
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("after guard boom")

	m := New[StateID, EventID, errData]("guard-err-after").
		Initial("waiting").
		State("waiting", func(s *SB) {
			s.After(time.Millisecond).
				GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", recordGuardErr).
				GoTo("ok")
		}).
		State("ok", func(*SB) {}).
		State("failed", func(*SB) {}).
		Build()

	bar, done := newErrTransitionBarrier(1)
	a := Start(m, errData{}, m.WithObservers(bar))
	defer a.Stop()

	waitErrSignal(t, done)

	if got := a.State(); got != "failed" {
		t.Fatalf("State() = %q, want %q", got, "failed")
	}
	if got := a.Data().Err; got != sentinel.Error() {
		t.Fatalf("Data().Err = %q, want %q", got, sentinel.Error())
	}
}

func TestGuardErr_ObserverReportsError(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("script type error")

	m := New[StateID, EventID, errData]("guard-err-obs").
		Initial("idle").
		State("idle", func(s *SB) {
			s.On("GO").
				GuardErr(func(errData) (bool, error) { return true, sentinel }, "failed", recordGuardErr).
				GoTo("ok")
			s.On("PLAIN").
				Guard(func(errData) bool { return true }).
				GoTo("ok")
		}).
		State("ok", func(*SB) {}).
		State("failed", func(*SB) {}).
		Build()

	t.Run("guard_err_payload_and_transition", func(t *testing.T) {
		rec := &RecordingObserver[StateID, EventID, errData]{}
		bar, done := newErrTransitionBarrier(1)
		a := Start(m, errData{}, m.WithObservers(rec, bar))
		defer a.Stop()

		a.Send("GO")
		waitErrSignal(t, done)

		guards := rec.Guards()
		if len(guards) != 1 {
			t.Fatalf("len(Guards()) = %d, want 1", len(guards))
		}
		ge := guards[0]
		if !errors.Is(ge.Err, sentinel) {
			t.Fatalf("GuardEvent.Err = %v, want %v", ge.Err, sentinel)
		}
		if ge.Result {
			t.Fatal("GuardEvent.Result = true, want false on error")
		}
		if ge.State != "idle" || ge.Event != "GO" || ge.Target != "ok" {
			t.Fatalf("GuardEvent = %+v, want State=idle Event=GO Target=ok", ge)
		}

		raw, err := json.Marshal(ge)
		if err != nil {
			t.Fatalf("json.Marshal(GuardEvent): %v", err)
		}
		if !strings.Contains(string(raw), `"error":"script type error"`) {
			t.Fatalf("GuardEvent JSON missing error field: %s", raw)
		}
		if !strings.Contains(ge.String(), "err=script type error") {
			t.Fatalf("GuardEvent.String() missing error text: %q", ge.String())
		}

		transitions := rec.Transitions()
		if len(transitions) != 1 {
			t.Fatalf("len(Transitions()) = %d, want 1", len(transitions))
		}
		te := transitions[0]
		if te.From != "idle" || te.To != "failed" || te.Event != "GO" {
			t.Fatalf("TransitionEvent = %+v, want From=idle To=failed Event=GO", te)
		}
	})

	t.Run("plain_guard_has_nil_err_and_omits_json_error", func(t *testing.T) {
		rec := &RecordingObserver[StateID, EventID, errData]{}
		bar, done := newErrTransitionBarrier(1)
		a := Start(m, errData{}, m.WithObservers(rec, bar))
		defer a.Stop()

		a.Send("PLAIN")
		waitErrSignal(t, done)

		guards := rec.Guards()
		if len(guards) != 1 {
			t.Fatalf("len(Guards()) = %d, want 1", len(guards))
		}
		ge := guards[0]
		if ge.Err != nil {
			t.Fatalf("plain GuardEvent.Err = %v, want nil", ge.Err)
		}
		if !ge.Result {
			t.Fatal("plain GuardEvent.Result = false, want true")
		}
		raw, err := json.Marshal(ge)
		if err != nil {
			t.Fatalf("json.Marshal(GuardEvent): %v", err)
		}
		if strings.Contains(string(raw), `"error"`) {
			t.Fatalf("plain GuardEvent JSON unexpectedly contains error key: %s", raw)
		}
		if strings.Contains(ge.String(), "err=") {
			t.Fatalf("plain GuardEvent.String() unexpectedly contains err=: %q", ge.String())
		}
	})
}

func TestGuardErr_HydrateAndComposition(t *testing.T) {
	type MB = MachineBuilder[StateID, EventID, errData]
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("plugin guard failed")

	plugin := func(mb *MB) {
		mb.State("failed", func(*SB) {})
		mb.Extend("work", func(w *SB) {
			w.ReplaceOnDone("next").
				GuardErr(func(errData) (bool, error) { return false, sentinel }, "failed", recordGuardErr).
				GoTo("next")
			w.OnDone().GoTo("next")
		})
	}

	m := New[StateID, EventID, errData]("guard-err-compose").
		Initial("work").
		State("work", func(w *SB) {
			w.Initial("step")
			w.State("step", func(s *SB) {
				s.On("FINISH").GoTo("done")
			})
			w.State("done", func(s *SB) { s.Type(Final) })
			w.OnDone().GoTo("next")
		}).
		State("next", func(*SB) {}).
		Use(plugin).
		Build()

	a1 := Start(m, errData{Count: 9})
	snap := a1.Snapshot()
	a1.Stop()

	bar, done := newErrTransitionBarrier(2)
	a2 := Hydrate(m, snap, m.WithObservers(bar))
	defer a2.Stop()

	a2.Send("FINISH")
	waitErrSignal(t, done)

	if got := a2.State(); got != "failed" {
		t.Fatalf("State() = %q, want %q", got, "failed")
	}
	if data := a2.Data(); data.Count != 9 || data.Err != sentinel.Error() {
		t.Fatalf("Data() = %+v, want Count=9 Err=%q", data, sentinel.Error())
	}
}

func TestGuardErr_ParallelRegionLocalErrorSettlesAndHydratesIdentically(t *testing.T) {
	type SB = StateBuilder[StateID, EventID, errData]
	sentinel := errors.New("local parallel guard boom")

	m := New[StateID, EventID, errData]("guard-err-par-region-local").
		Initial("p").
		State("p", func(p *SB) {
			p.Type(Parallel)
			p.State("a", func(r *SB) {
				r.Initial("a_work")
				r.State("a_work", func(w *SB) {
					w.Initial("a1")
					w.State("a1", func(s *SB) {
						s.On("GO").GoTo("a_inner_done")
					})
					w.State("a_inner_done", func(s *SB) { s.Type(Final) })
					w.OnDone().Assign(appendErrTrail("a_ondone")).GoTo("a_step")
				})
				r.State("a_step", func(s *SB) {
					s.Always().Assign(appendErrTrail("a_always")).GoTo("a_done")
				})
				r.State("a_done", func(s *SB) { s.Type(Final) })
			})
			p.State("b", func(r *SB) {
				r.Initial("b1")
				r.State("b1", func(s *SB) {
					s.On("GO").GoTo("b_sub")
				})
				r.State("b_sub", func(sub *SB) {
					sub.Initial("check")
					sub.State("check", func(s *SB) {
						s.Always().
							GuardErr(func(errData) (bool, error) { return false, sentinel }, "bFailed", recordGuardErr).
							GoTo("b_done")
					})
				})
				r.State("b_done", func(s *SB) { s.Type(Final) })
				r.State("bFailed", func(*SB) {})
			})
			p.OnDone().GoTo("all_done")
		}).
		State("all_done", func(*SB) {}).
		Build()

	settled := make(chan struct{}, 1)
	syncObs := ObserverFuncs[StateID, EventID, errData]{
		EventDroppedFunc: func(_ context.Context, e *EventNotice[StateID, EventID, errData]) {
			if e.Event == "SYNC" {
				select {
				case settled <- struct{}{}:
				default:
				}
			}
		},
	}

	a := Start(m, errData{}, m.WithObservers(syncObs))
	defer a.Stop()

	a.Send("GO")
	a.Send("SYNC")
	waitErrSignal(t, settled)

	// Non-Final region-local error target keeps the enclosing parallel state incomplete,
	// while sibling region 'a' settles its queued OnDone and Always transitions normally.
	if a.Matches("all_done") {
		t.Fatalf("enclosing parallel OnDone fired despite non-Final bFailed; active=%v", a.States())
	}
	wantStates := []StateID{"p", "a", "b", "a_done", "bFailed"}
	if !slices.Equal(a.States(), wantStates) {
		t.Fatalf("live States() = %v, want %v", a.States(), wantStates)
	}
	wantTrail := []string{"a_ondone", "a_always"}
	if !slices.Equal(a.Data().Trail, wantTrail) {
		t.Fatalf("live Data().Trail = %v, want %v", a.Data().Trail, wantTrail)
	}
	if a.Data().Err != sentinel.Error() {
		t.Fatalf("live Data().Err = %q, want %q", a.Data().Err, sentinel.Error())
	}

	// Snapshot + Hydrate must settle to the exact same configuration and data.
	snap := a.Snapshot()
	a2 := Hydrate(m, snap)
	defer a2.Stop()

	if !slices.Equal(a2.States(), a.States()) {
		t.Fatalf("hydrated States() = %v, live States() = %v", a2.States(), a.States())
	}
	if a2.Data().Err != a.Data().Err || !slices.Equal(a2.Data().Trail, a.Data().Trail) {
		t.Fatalf("hydrated Data() = %+v, live Data() = %+v", a2.Data(), a.Data())
	}
}
