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
				if route, _ := a.evalGuard(ctx, id, zero, t); route != nil {
					source, selected = id, route
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
				if route, _ := a.evalGuard(ctx, occurrence.state, zero, t); route != nil {
					source, selected = occurrence.state, route
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
