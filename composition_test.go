package gstate

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuilderExtendAndUse(t *testing.T) {
	const (
		StateRoot       StateID = "root"
		StateIdle       StateID = "idle"
		StateActiveTurn StateID = "turn"
		StateCallLLM    StateID = "call_llm"
		StateToolGate   StateID = "tool_gate"
		StateExecTool   StateID = "exec_tool"
		StateCompacting StateID = "compacting"

		EventPrompt   EventID = "PROMPT"
		EventOverflow EventID = "OVERFLOW"
		EventApprove  EventID = "APPROVE"
	)

	// Plugin 1: Compaction plugin extends root and active turn without overwriting them.
	compactionPlugin := func(m *MachineBuilder[StateID, EventID, Context]) {
		m.Extend(StateRoot, func(root *StateBuilder[StateID, EventID, Context]) {
			root.State(StateCompacting, func(c *StateBuilder[StateID, EventID, Context]) {
				c.On(EventPrompt).GoTo(StateActiveTurn)
			})
			root.Extend(StateActiveTurn, func(turn *StateBuilder[StateID, EventID, Context]) {
				turn.History(Deep)
				turn.On(EventOverflow).GoTo(StateCompacting)
			})
		})
	}

	// Plugin 2: Policy gate plugin mounted via StateBuilder.Use.
	policyGatePlugin := func(gate *StateBuilder[StateID, EventID, Context]) {
		gate.Entry(func(c Context) Context {
			c.Count += 10
			return c
		})
		gate.On(EventApprove).GoTo(StateExecTool)
	}

	m := New[StateID, EventID, Context]("harness").
		Initial(StateRoot).
		State(StateRoot, func(root *StateBuilder[StateID, EventID, Context]) {
			root.Initial(StateIdle)
			root.State(StateIdle, func(idle *StateBuilder[StateID, EventID, Context]) {
				idle.On(EventPrompt).GoTo(StateActiveTurn)
			})
			root.State(StateActiveTurn, func(turn *StateBuilder[StateID, EventID, Context]) {
				turn.Initial(StateCallLLM)
				turn.State(StateCallLLM, func(llm *StateBuilder[StateID, EventID, Context]) {
					llm.Always().GoTo(StateToolGate)
				})
				turn.State(StateToolGate, func(gate *StateBuilder[StateID, EventID, Context]) {
					gate.Use(policyGatePlugin)
				})
				turn.State(StateExecTool, func(exec *StateBuilder[StateID, EventID, Context]) {})
			})
		}).
		Use(compactionPlugin).
		Build()

	transitions := make(chan StateID, 10)
	obs := ObserverFuncs[StateID, EventID, Context]{
		TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
			transitions <- e.To
		},
	}
	waitTrans := func(want StateID) {
		t.Helper()
		for {
			select {
			case got := <-transitions:
				if got == want {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for transition to %q", want)
			}
		}
	}

	actor := Start(m, Context{}, m.WithObservers(obs))
	defer actor.Stop()

	// Prompt transitions idle -> turn -> call_llm -> tool_gate
	actor.Send(EventPrompt)
	waitTrans(StateToolGate)

	if !actor.Matches(StateToolGate) {
		t.Fatalf("expected actor in %q, got %v", StateToolGate, actor.States())
	}
	if got := actor.Data().Count; got != 10 {
		t.Fatalf("expected Count=10 from policyGatePlugin Entry, got %d", got)
	}

	// Trigger overflow interrupt added by compactionPlugin via Extend
	actor.Send(EventOverflow)
	waitTrans(StateCompacting)
	if !actor.Matches(StateCompacting) {
		t.Fatalf("expected actor in %q, got %v", StateCompacting, actor.States())
	}

	// Resume turn; Deep history added via Extend should restore StateToolGate
	actor.Send(EventPrompt)
	waitTrans(StateActiveTurn)
	if !actor.Matches(StateToolGate) {
		t.Fatalf("expected Deep history to restore %q, got %v", StateToolGate, actor.States())
	}

	// Approve transitions tool_gate -> exec_tool
	actor.Send(EventApprove)
	waitTrans(StateExecTool)
	if !actor.Matches(StateExecTool) {
		t.Fatalf("expected actor in %q, got %v", StateExecTool, actor.States())
	}
}

func TestBuilderExtendPanicsOnInvalidTarget(t *testing.T) {
	type B = StateBuilder[string, string, Context]

	tests := []struct {
		name  string
		build func()
	}{
		{"empty top-level extend", func() {
			New[string, string, Context]("x").Extend("", func(*B) {})
		}},
		{"missing top-level extend", func() {
			New[string, string, Context]("x").Extend("missing", func(*B) {})
		}},
		{"top-level extend on nested child", func() {
			New[string, string, Context]("x").
				State("p", func(s *B) {
					s.Initial("c")
					s.State("c", func(*B) {})
				}).
				Extend("c", func(*B) {})
		}},
		{"nested extend on empty child", func() {
			New[string, string, Context]("x").
				State("p", func(s *B) {
					s.Extend("", func(*B) {})
				})
		}},
		{"nested extend on missing child", func() {
			New[string, string, Context]("x").
				State("p", func(s *B) {
					s.Extend("missing", func(*B) {})
				})
		}},
		{"nested extend on sibling state", func() {
			New[string, string, Context]("x").
				State("p", func(s *B) {
					s.Initial("a")
					s.State("a", func(*B) {})
				}).
				State("q", func(s *B) {
					s.Extend("a", func(*B) {})
				})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertPanic(t, tc.build, "gstate:")
		})
	}
}

func TestSendWithAndSendCtxWith(t *testing.T) {
	m := New[StateID, EventID, Context]("payload-test").
		Initial("idle").
		State("idle", func(s *StateBuilder[StateID, EventID, Context]) {
			// Guard inspects the payload applied by SendWith/SendCtxWith!
			s.On("SUBMIT").
				Guard(func(c Context) bool { return c.Count >= 10 }).
				Assign(func(c Context) Context {
					c.Count += 1 // transition Assign composes on top of payload
					return c
				}).
				GoTo("accepted")
		}).
		State("accepted", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Entry(func(c Context) Context {
				c.Count *= 2
				return c
			})
		}).
		Build()

	droppedCh := make(chan EventID, 4)
	transCh := make(chan StateID, 4)
	obs := ObserverFuncs[StateID, EventID, Context]{
		EventDroppedFunc: func(_ context.Context, e *EventNotice[StateID, EventID, Context]) {
			droppedCh <- e.Event
		},
		TransitionFunc: func(_ context.Context, e *TransitionEvent[StateID, EventID, Context]) {
			transCh <- e.To
		},
	}
	actor := Start(m, Context{Count: 1}, m.WithObservers(obs))
	defer actor.Stop()

	// 1. SendWith where Guard fails (Count=5 < 10): event is dropped AND payload mutation is rolled back!
	actor.SendWith("SUBMIT", func(c Context) Context {
		c.Count = 5
		return c
	})
	select {
	case <-droppedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dropped SUBMIT")
	}
	if got := actor.Data().Count; got != 1 {
		t.Fatalf("dropped SendWith must not dirty actor data: got Count=%d, want 1", got)
	}

	// 2. SendWith for an unhandled event in current state: dropped AND rolled back!
	actor.SendWith("UNKNOWN_EVENT", func(c Context) Context {
		c.Count = 999
		return c
	})
	select {
	case <-droppedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dropped UNKNOWN_EVENT")
	}
	if got := actor.Data().Count; got != 1 {
		t.Fatalf("unhandled SendWith must not dirty actor data: got Count=%d, want 1", got)
	}

	// 3. SendCtxWith where Guard passes (Count=10 >= 10):
	// Payload (10) -> Assign (+1 = 11) -> Entry (*2 = 22).
	if err := actor.SendCtxWith(context.Background(), "SUBMIT", func(c Context) Context {
		c.Count = 10
		return c
	}); err != nil {
		t.Fatalf("SendCtxWith returned unexpected error: %v", err)
	}
	select {
	case <-transCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transition to accepted")
	}

	if got := actor.State(); got != "accepted" {
		t.Fatalf("state=%q, want accepted", got)
	}
	if got := actor.Data().Count; got != 22 {
		t.Fatalf("Count=%d, want 22", got)
	}

	// 4. SendCtxWith with cancelled context returns context.Canceled.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := actor.SendCtxWith(cancelCtx, "SUBMIT", func(c Context) Context { return c }); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// 5. SendCtxWith after Stop returns ErrActorStopped.
	actor.Stop()
	if err := actor.SendCtxWith(context.Background(), "SUBMIT", func(c Context) Context { return c }); !errors.Is(err, ErrActorStopped) {
		t.Fatalf("expected ErrActorStopped, got %v", err)
	}
}

func TestHydrateWithHydrateServicesDisabled(t *testing.T) {
	var invokeRuns atomic.Int32
	enteredNext := make(chan struct{}, 1)

	m := New[StateID, EventID, Context]("hydrate-no-invoke").
		Initial("exec_tool").
		State("exec_tool", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				invokeRuns.Add(1)
				<-ctx.Done()
				return ctx.Err()
			}, "done", "err")
			s.On("RETRY").GoTo("exec_tool_2")
		}).
		State("exec_tool_2", func(s *StateBuilder[StateID, EventID, Context]) {
			s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
				invokeRuns.Add(1)
				select {
				case enteredNext <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return ctx.Err()
			}, "done", "err")
		}).
		State("done", func(*StateBuilder[StateID, EventID, Context]) {}).
		State("err", func(*StateBuilder[StateID, EventID, Context]) {}).
		Build()

	snap := Snapshot[StateID, Context]{
		Active: []StateID{"exec_tool"},
		Data:   Context{Count: 42},
	}

	// Hydrate with WithHydrateServices(false): the restored "exec_tool" state must NOT start its Invoke.
	actor := Hydrate(m, snap, m.WithHydrateServices(false))
	defer actor.Stop()

	if got := invokeRuns.Load(); got != 0 {
		t.Fatalf("expected 0 invoke runs on Hydrate with WithHydrateServices(false), got %d", got)
	}
	if got := actor.State(); got != "exec_tool" {
		t.Fatalf("expected restored state exec_tool, got %q", got)
	}

	// Subsequent transition into a new state ("exec_tool_2") MUST start its Invoke normally.
	actor.Send("RETRY")
	select {
	case <-enteredNext:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for exec_tool_2 Invoke to start")
	}
	if got := invokeRuns.Load(); got != 1 {
		t.Fatalf("expected 1 invoke run after transitioning to exec_tool_2, got %d", got)
	}
}
