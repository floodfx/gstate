package gstate

import (
	"context"
	"sync"
	"testing"
)

// TestObserverConcurrentSendAndRead fans out concurrent SendCtx calls while a
// parallel goroutine reads RecordingObserver.Events(). It is the race-detector
// gate from the plan's M8 milestone. Synchronisation is deterministic: a
// kindBarrier blocks until every PING has been observed as a Transition.
func TestObserverConcurrentSendAndRead(t *testing.T) {
	rec := &RecordingObserver[StateID, EventID, Context]{}
	const senders = 8
	const perSender = 50
	const total = senders * perSender
	bar := newKindBarrier(KindTransition, total)

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
		m.WithObservers(rec, bar),
		m.WithMailboxSize(1024),
	)
	defer a.Stop()

	// Readers: poll Events() concurrently with writers.
	stopReader := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
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

	var senderWG sync.WaitGroup
	senderWG.Add(senders)
	for i := 0; i < senders; i++ {
		go func() {
			defer senderWG.Done()
			for j := 0; j < perSender; j++ {
				_ = a.SendCtx(context.Background(), "PING")
			}
		}()
	}
	senderWG.Wait()

	<-bar.done // deterministic: every PING has produced a Transition

	close(stopReader)
	readerWG.Wait()

	if got := len(rec.Transitions()); got != total {
		t.Errorf("recorded %d transitions, want %d", got, total)
	}
}
