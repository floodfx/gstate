package gstate

import (
	"testing"
	"time"
)

func TestSelfTransitionReEntry(t *testing.T) {
	entryCount := 0
	exitCount := 0

	machine := New[string, string, any]("self-trans").
		Initial("active").
		State("active", func(s *StateBuilder[string, string, any]) {
			s.Entry(func(c any) any {
				entryCount++
				return c
			})
			s.Exit(func(c any) any {
				exitCount++
				return c
			})
			s.On("RETRY").GoTo("active") // Self-transition
		}).
		Build()

	actor := Start(machine, nil)
	// Initial entry
	if entryCount != 1 {
		t.Errorf("Expected initial entry count 1, got %d", entryCount)
	}

	actor.Send("RETRY")
	time.Sleep(10 * time.Millisecond)

	// In a proper external self-transition, we expect Exit then Entry again.
	// Current implementation likely treats this as internal (no exit/entry).
	if exitCount != 1 {
		t.Errorf("Expected exit count 1 (self-transition), got %d", exitCount)
	}
	if entryCount != 2 {
		t.Errorf("Expected entry count 2 (re-entry), got %d", entryCount)
	}
}

func TestInfiniteLoopCircuitBreaker(t *testing.T) {
	// This test is designed to FAIL (hang) if the circuit breaker is missing.
	// We'll use a timeout to detect the hang.
	
	done := make(chan bool)
	
	go func() {
		machine := New[string, string, any]("loop").
			Initial("A").
			State("A", func(s *StateBuilder[string, string, any]) {
				s.Always().GoTo("B")
			}).
			State("B", func(s *StateBuilder[string, string, any]) {
				s.Always().GoTo("A")
			}).
			Build()

		Start(machine, nil)
		// If Start() blocks forever or handleAlwaysInternal loops forever, we won't reach here
		// Note: Start runs loop in goroutine, but handleAlwaysInternal runs synchronously 
		// during initial entry!
		
		done <- true
	}()

	select {
	case <-done:
		// Success (it didn't hang forever, though it might panic if we add a limit)
	case <-time.After(100 * time.Millisecond):
		t.Error("Test timed out - likely infinite loop in Always transitions")
	}
}
