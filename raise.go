package gstate

import (
	"context"
	"time"
)

// RaiseHandler receives events raised by transitions declared with
// [TransitionBuilder.Raise]. See [Machine.WithRaiseHandler].
type RaiseHandler[S ~string, E ~string] func(context.Context, RaisedEvent[S, E])

// pendingRaise is a raised event committed under the actor lock and awaiting
// delivery by the raise dispatcher.
type pendingRaise[S ~string, E ~string] struct {
	ctx   context.Context
	event RaisedEvent[S, E]
}

// WithRaiseHandler returns an [Option] that installs the handler receiving
// events raised by transitions declared with [TransitionBuilder.Raise], such
// as s.After(d).Raise(event). Use it to forward events to an external system
// (a message bus, a parent actor, a UI). Without a handler, raises are no-ops.
//
// Delivery contract:
//
//   - Raises are queued in commit order when their transition fires and are
//     delivered asynchronously, one at a time and in that order, from a
//     dedicated goroutine that does not hold the actor lock. The handler may
//     therefore read the actor (e.g. [Actor.State]) and call [Actor.Send].
//   - The handler receives the triggering context with cancellation removed,
//     so request-scoped values (trace IDs) survive.
//   - A slow handler delays later raises but never blocks the actor.
//   - Raises committed before shutdown are still delivered; [Actor.Stop]
//     waits for them. The handler must therefore not call Stop
//     synchronously; schedule it from another goroutine when needed.
func (m *Machine[S, E, D]) WithRaiseHandler(fn RaiseHandler[S, E]) Option[S, E, D] {
	return func(c *config[S, E, D]) { c.raiseHandler = fn }
}

// queueRaisesLocked records t's raised events for delivery. Must be called
// with a.mu held.
func (a *Actor[S, E, D]) queueRaisesLocked(ctx context.Context, source S, t *TransitionDef[S, E, D]) {
	if a.raiseHandler == nil || len(t.Raise) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	now := time.Now()
	for _, ev := range t.Raise {
		a.pendingRaises = append(a.pendingRaises, pendingRaise[S, E]{
			ctx: ctx,
			event: RaisedEvent[S, E]{
				MachineID: a.machine.ID,
				ActorID:   a.id,
				Source:    source,
				Target:    t.Target,
				Event:     ev,
				Timestamp: now,
			},
		})
	}
	select {
	case a.raiseSignal <- struct{}{}:
	default:
	}
}

// startRaiseDispatcher launches the delivery goroutine when a handler is set.
func (a *Actor[S, E, D]) startRaiseDispatcher() {
	if a.raiseHandler == nil {
		return
	}
	a.raiseSignal = make(chan struct{}, 1)
	a.wg.Add(1)
	go a.dispatchRaises()
}

func (a *Actor[S, E, D]) dispatchRaises() {
	defer a.wg.Done()
	for {
		select {
		case <-a.raiseSignal:
			a.deliverPendingRaises()
		case <-a.stopped:
			// No raise can be queued after stopped closes: every queuing path
			// checks stopped under the lock. Drain what was committed before.
			a.deliverPendingRaises()
			return
		}
	}
}

func (a *Actor[S, E, D]) deliverPendingRaises() {
	for {
		a.mu.Lock()
		pending := a.pendingRaises
		a.pendingRaises = nil
		a.mu.Unlock()
		if len(pending) == 0 {
			return
		}
		for _, p := range pending {
			a.raiseHandler(p.ctx, p.event)
		}
	}
}
