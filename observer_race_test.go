package gstate

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestObserverConcurrentSendAndRead fans out concurrent SendCtx calls while a
// parallel goroutine reads RecordingObserver.Events(). It is the race-detector
// gate from the plan's M8 milestone.
func TestObserverConcurrentSendAndRead(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	m := New[StateID, EventID, Context]("race").
		Initial("a").
		State("a", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("PING").GoTo("b")
		}).
		State("b", func(s *StateBuilder[StateID, EventID, Context]) {
			s.On("PING").GoTo("a")
		}).
		Build()

	a := Start(m, Context{},
		WithObserver[StateID, EventID, Context](rec),
		WithMailboxSize[StateID, EventID, Context](1024),
	)
	defer a.Stop()

	const senders = 8
	const perSender = 50

	var wg sync.WaitGroup
	wg.Add(senders + 1)

	// Readers: poll Events() while writes happen.
	stopReader := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopReader:
				return
			default:
				_ = rec.Events()
				_ = rec.Transitions()
			}
		}
	}()

	for i := 0; i < senders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perSender; j++ {
				a.SendCtx(context.Background(), "PING")
			}
		}()
	}

	// Give the actor time to drain.
	time.Sleep(200 * time.Millisecond)
	close(stopReader)
	wg.Wait()

	if got := len(rec.Transitions()); got == 0 {
		t.Errorf("expected some transitions to be recorded, got 0")
	}
}
