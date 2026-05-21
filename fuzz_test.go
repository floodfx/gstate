package gstate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fuzzMachine builds a small but structurally varied machine that
// exercises compound nesting, parallel regions, history, final states,
// and event-driven transitions. Used as the fixed machine target for
// FuzzHydrate and FuzzEventSequence.
func fuzzMachine() *Machine[StateID, EventID, Context] {
	return New[StateID, EventID, Context]("fuzz").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("START").GoTo("working")
		}).
		State("working", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Initial("loading")
			s.History(Shallow)
			s.State("loading", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("DONE").GoTo("ready")
				s.On("FAIL").GoTo("error")
			})
			s.State("ready", func(s *StateBuilder[StateID, EventID, Context]) {
				s.On("STOP").GoTo("idle")
				s.On("FAIL").GoTo("error")
			})
			s.State("error", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Type(Final)
			})
		}).
		State("parallel", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Type(Parallel)
			s.State("region1", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r1a")
				s.State("r1a", func(_ *StateBuilder[StateID, EventID, Context]) {})
				s.State("r1b", func(_ *StateBuilder[StateID, EventID, Context]) {})
			})
			s.State("region2", func(s *StateBuilder[StateID, EventID, Context]) {
				s.Initial("r2a")
				s.State("r2a", func(_ *StateBuilder[StateID, EventID, Context]) {})
				s.State("r2b", func(_ *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Build()
}

// FuzzHydrate feeds fuzzer-controlled bytes through json.Unmarshal into
// a Snapshot and calls Hydrate against a fixed machine. The invariant
// is "no panic" — invalid snapshots should either succeed quietly,
// fail in a recoverable way, or be rejected, but never panic.
//
// Persisted snapshots come from disk or network and can be corrupted
// or attacker-influenced, so this surface is real.
func FuzzHydrate(f *testing.F) {
	// Seed with a few legitimate snapshots produced by a live actor,
	// plus an obviously-broken one.
	m := fuzzMachine()
	a := Start(m, Context{Count: 0})
	good, _ := json.Marshal(a.Snapshot())
	a.Stop()

	f.Add(good)
	f.Add([]byte(`{"active":[]}`))
	f.Add([]byte(`{"active":["idle"]}`))
	f.Add([]byte(`{"active":["nonexistent_state"]}`))
	f.Add([]byte(`{"active":["working"],"history":{"working":"loading"}}`))
	f.Add([]byte(`{"active":["working","loading","idle"]}`))
	// Regression: nil History (no history field) + transition that
	// exits a state with non-empty parent → panics if Hydrate didn't
	// coalesce nil to an empty map. Triggers via Hydrate(active=loading)
	// then SendCtx("DONE") which exits loading (parent=working) and
	// writes history["working"]="loading".
	f.Add([]byte(`{"active":["loading"]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var snap Snapshot[StateID, Context]
		if err := json.Unmarshal(data, &snap); err != nil {
			// Unmarshal failures are fine; we only care about Hydrate.
			return
		}
		a := Hydrate(m, snap)
		defer a.Stop()
		_ = a.SendCtx(context.Background(), "START")
		_ = a.SendCtx(context.Background(), "DONE")
	})
}

// FuzzBuilder drives the [MachineBuilder] / [StateBuilder] API from
// fuzzer-controlled bytes encoding a sequence of state declarations.
// Each byte chunk decodes into one state and its transition target.
// The invariant is "Build returns a valid machine or panics" — and
// the fuzzer should never find a panic.
func FuzzBuilder(f *testing.F) {
	// Encoding: groups of 4 bytes per state declaration. Each group is
	// (idIdx, parentIdx, type, transitionTargetIdx) where idx values
	// modulo a small pool (a..h) select a state ID and "type" mod 4
	// picks Atomic / Compound / Parallel / Final.

	f.Add([]byte{0, 0xff, 0, 1, 1, 0xff, 0, 0}) // two top-level atomic states
	f.Add([]byte{0, 0xff, 1, 0xff, 1, 0, 0, 1}) // compound a with child b
	f.Add([]byte{})

	pool := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				var errStr string
				switch v := r.(type) {
				case error:
					errStr = v.Error()
				case string:
					errStr = v
				default:
					panic(r)
				}
				if !strings.HasPrefix(errStr, "gstate:") {
					panic(r) // re-panic unexpected failures
				}
			}
		}()
		if len(data) < 4 {
			return
		}
		const stride = 4
		nStates := len(data) / stride
		if nStates < 1 || nStates > 32 {
			return
		}

		mb := New[StateID, EventID, Context]("fuzz_builder")
		// Set Initial to the first state in the program.
		mb.Initial(StateID(pool[int(data[0])%len(pool)]))

		// We model the structure as a flat list with each state
		// optionally nested under a previously-declared parent. The
		// fuzzer-controlled "parent" byte references a previously-
		// declared state (by its index in the program) or 0xff for
		// "top-level".
		seen := map[int]*StateBuilder[StateID, EventID, Context]{}

		declare := func(parentIdx int, idx int, id StateID, typeByte byte, target StateID) {
			fn := func(s *StateBuilder[StateID, EventID, Context]) {
				switch typeByte % 4 {
				case 0:
					// Atomic (default) — but if it also has a target,
					// add an outgoing transition.
				case 1:
					// Compound — needs an Initial; reuse this state's
					// own id as the initial child target. The fuzzer
					// will probably not satisfy this validly; that's
					// the point.
					s.Initial(target)
				case 2:
					s.Type(Parallel)
				case 3:
					s.Type(Final)
				}
				if target != "" {
					s.On("EV").GoTo(target)
				}
				seen[idx] = s
			}
			if parentIdx == 0xff {
				mb.State(id, fn)
				return
			}
			parent, ok := seen[parentIdx%len(seen)+0]
			if !ok || len(seen) == 0 {
				mb.State(id, fn)
				return
			}
			parent.State(id, fn)
		}

		for i := 0; i < nStates; i++ {
			off := i * stride
			idIdx := int(data[off]) % len(pool)
			parentByte := data[off+1]
			typeByte := data[off+2]
			targetIdx := int(data[off+3]) % len(pool)
			id := StateID(pool[idIdx])
			target := StateID(pool[targetIdx])

			parentIdx := 0xff
			if parentByte != 0xff && i > 0 {
				parentIdx = int(parentByte) % i
			}
			declare(parentIdx, i, id, typeByte, target)
		}

		_ = mb.Build()
	})
}

// FuzzEventSequence runs a fixed machine through a fuzzer-controlled
// event sequence and checks invariants: no panic, the active set is
// never empty while the actor is running, and OnEventReceived count
// equals the number of events actually delivered.
func FuzzEventSequence(f *testing.F) {
	pool := []EventID{"START", "DONE", "FAIL", "STOP", "BOGUS"}

	f.Add([]byte{0, 1, 3, 0})         // START DONE STOP START
	f.Add([]byte{0, 1, 2})            // START DONE FAIL (hits Final)
	f.Add([]byte{0xff, 0xff, 0xff})   // garbage indices
	f.Add([]byte{4, 4, 4, 4})         // all BOGUS — should be dropped

	m := fuzzMachine()
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 128 {
			data = data[:128]
		}

		rec := &RecordingObserver[StateID, EventID, Context]{}
		a := Start(m, Context{}, m.WithObservers(rec))
		defer a.Stop()

		// SendCtx with a short timeout so a wedged mailbox doesn't hang
		// the fuzzer iteration. fuzzMachine has no slow actions/invokes
		// so this should never actually fire.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		delivered := 0
		for _, b := range data {
			ev := pool[int(b)%len(pool)]
			if err := a.SendCtx(ctx, ev); err == nil {
				delivered++
			} else {
				// Either ctx expired, or the actor auto-stopped via a
				// Final state. Both are acceptable; stop sending.
				break
			}
			// Invariant: active set is non-empty while running.
			if states := a.States(); len(states) == 0 {
				t.Fatalf("active set empty after sending %q (data so far=%v)", ev, data)
			}
		}

		// Drain: the events we sent are processed asynchronously by the
		// loop goroutine. Stop blocks on wg.Wait so by the time Stop
		// returns, every delivered event has been processed (or the
		// actor was already stopped, in which case delivered would be
		// lower than the events we tried to send).
		a.Stop()

		// Invariant: OnEventReceived count cannot exceed delivered. We
		// don't require equality because the loop may have abandoned
		// queued events on Stop per the post-#2 contract.
		recvCount := len(rec.EventsReceived())
		if recvCount > delivered {
			t.Fatalf("OnEventReceived=%d > delivered=%d (data=%v)", recvCount, delivered, data)
		}
	})
}
