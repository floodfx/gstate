package gstate

import (
	"sync/atomic"
	"testing"
)

func TestSelfTransitionReEntry(t *testing.T) {
	// Counters are mutated from the actor goroutine inside Entry/Exit and read
	// from the test goroutine. Use atomic.Int32 so -race stays clean.
	var entryCount, exitCount atomic.Int32

	// reentered is closed when the self-transition's re-entry fires, so the
	// test waits deterministically without a sleep.
	reentered := make(chan struct{})

	machine := New[string, string, Context]("self-trans").
		Initial("active").
		State("active", func(s *StateBuilder[string, string, Context]) {
			s.Entry(func(c Context) Context {
				if entryCount.Add(1) == 2 {
					close(reentered)
				}
				return c
			})
			s.Exit(func(c Context) Context {
				exitCount.Add(1)
				return c
			})
			s.On("RETRY").GoTo("active") // Self-transition
		}).
		Build()

	actor := Start(machine, Context{})
	defer actor.Stop()

	// Initial entry happens synchronously inside Start.
	if got := entryCount.Load(); got != 1 {
		t.Errorf("Expected initial entry count 1, got %d", got)
	}

	actor.Send("RETRY")
	<-reentered // blocks until the re-entry's Entry closure has run

	// In a proper external self-transition, we expect Exit then Entry again.
	if got := exitCount.Load(); got != 1 {
		t.Errorf("Expected exit count 1 (self-transition), got %d", got)
	}
	if got := entryCount.Load(); got != 2 {
		t.Errorf("Expected entry count 2 (re-entry), got %d", got)
	}
}
