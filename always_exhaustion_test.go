package gstate

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestAlwaysExhaustionStopsServices(t *testing.T) {
	cancelled := make(chan struct{})
	m := New[string, string, Context]("cycle").Initial("a").State("a", func(s *StateBuilder[string, string, Context]) {
		s.Invoke(func(ctx context.Context, _ Context, _ func(func(Context) Context)) error {
			<-ctx.Done()
			close(cancelled)
			return nil
		}, "", "")
		s.Always().Assign(func(c Context) Context { c.Count++; return c })
	}).Build()
	a := Start(m, Context{})
	defer a.Stop()
	select {
	case <-a.stopped:
	default:
		t.Fatal("Always exhaustion left actor running")
	}
	if !errors.Is(a.Err(), ErrAlwaysLimit) {
		t.Fatalf("Err()=%v", a.Err())
	}
	<-cancelled
	if got := a.Data().Count; got != 100 {
		t.Fatalf("actions=%d, want 100", got)
	}
}

func TestLongFiniteAlwaysChain(t *testing.T) {
	m := New[string, string, Context]("long").Initial("s0")
	for i := 0; i < 250; i++ {
		id, next := fmt.Sprintf("s%d", i), fmt.Sprintf("s%d", i+1)
		m.State(id, func(s *StateBuilder[string, string, Context]) {
			s.Always().Assign(func(c Context) Context { c.Count++; return c }).GoTo(next)
		})
	}
	m.State("s250", func(s *StateBuilder[string, string, Context]) { s.Type(Final) })
	a := Start(m.Build(), Context{})
	defer a.Stop()
	if a.Err() != nil {
		t.Fatalf("finite chain error=%v", a.Err())
	}
	if got := a.State(); got != "s250" {
		t.Fatalf("state=%q, want s250", got)
	}
	if got := a.Data().Count; got != 250 {
		t.Fatalf("actions=%d, want 250", got)
	}
}
