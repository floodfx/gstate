package gstate

import (
	"context"
	"slices"
)

type completionOccurrence[S ~string] struct {
	state S
	gen   uint64
}

// flushFinalEntries runs after a whole event batch or entry tree, never while
// sibling parallel regions are still being entered or processing the same event.
func (a *Actor[S, E, D]) flushFinalEntries() {
	entries := a.pendingFinalEntries
	a.pendingFinalEntries = nil
	sources := make(map[S]uint64)
	for _, entry := range entries {
		if !a.active[entry.state] || a.stateGens[entry.state] != entry.gen {
			continue
		}
		parent := a.machine.States[entry.state].parent
		for parent != "" && a.isStateDoneLocked(parent) {
			sources[parent] = a.stateGens[parent]
			next := a.machine.States[parent].parent
			if next == "" || a.machine.States[next].Type != Parallel {
				break
			}
			parent = next
		}
	}
	ids := sortedStateIDs(sources)
	slices.SortStableFunc(ids, func(x, y S) int {
		return a.machine.States[y].depth - a.machine.States[x].depth
	})
	for _, id := range ids {
		occurrence := completionOccurrence[S]{id, sources[id]}
		if !slices.Contains(a.pendingCompletions, occurrence) {
			a.pendingCompletions = append(a.pendingCompletions, occurrence)
		}
	}
}

func (a *Actor[S, E, D]) completionGuard(ctx context.Context, source S, t *TransitionDef[S, E, D]) bool {
	if t.Guard == nil {
		return true
	}
	ok := t.Guard(a.data)
	if len(a.guardObs) > 0 {
		var zero E
		e := a.newGuardEvent(source, zero, t.Target, ok)
		for _, o := range a.guardObs {
			o.OnGuardEvaluated(ctx, e)
		}
	}
	return ok
}

// stabilizeLocked settles eventless transitions before dispatching each queued
// completion. All callers hold the actor lock and have finished their event batch.
func (a *Actor[S, E, D]) stabilizeLocked(ctx context.Context) {
	budget := max(100, 2*len(a.machine.States))
	steps := 0
	var zero E
	for {
		a.flushFinalEntries()
		var source S
		var selected *TransitionDef[S, E, D]
		for _, id := range a.getSortedActiveStatesLocked() {
			sd := a.machine.States[id]
			if sd == nil {
				continue
			}
			for _, t := range sd.Always {
				if a.completionGuard(ctx, id, t) {
					source, selected = id, t
					break
				}
			}
			if selected != nil {
				break
			}
		}
		if selected == nil {
			if len(a.pendingCompletions) == 0 {
				return
			}
			occurrence := a.pendingCompletions[0]
			a.pendingCompletions = a.pendingCompletions[1:]
			if a.stateGens[occurrence.state] != occurrence.gen || !a.isStateDoneLocked(occurrence.state) {
				continue
			}
			for _, t := range a.machine.States[occurrence.state].OnDone {
				if a.completionGuard(ctx, occurrence.state, t) {
					source, selected = occurrence.state, t
					break
				}
			}
			if selected == nil {
				continue
			}
		}
		if steps == budget {
			a.terminalErr = ErrAlwaysLimit
			a.initiateShutdownLocked()
			return
		}
		steps++
		a.executeTransition(ctx, source, selected, zero)
	}
}
