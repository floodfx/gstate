package gstate

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Actor is the runtime interpreter for a statechart machine.
// It maintains the current state, processes events sequentially,
// and manages asynchronous services (invocations and timers).
type Actor[S ~string, E ~string, C any] struct {
	machine     *Machine[S, E, C]
	context     C
	active      map[S]bool
	history     map[S]S
	invocations map[S]context.CancelFunc
	timers      map[S][]*time.Timer
	mailbox     chan E
	mu          sync.RWMutex
	stopOnce    sync.Once
}

// Options defines configuration parameters for an Actor.
type Options struct {
	// MailboxSize sets the buffer size for the event channel. Defaults to 100.
	MailboxSize int
}

// Start creates and launches a new Actor instance for the given machine with default options.
func Start[S ~string, E ~string, C any](m *Machine[S, E, C], initialContext C) *Actor[S, E, C] {
	return StartWithOptions(m, initialContext, Options{MailboxSize: 100})
}

// StartWithOptions creates and launches a new Actor instance with custom options.
func StartWithOptions[S ~string, E ~string, C any](m *Machine[S, E, C], initialContext C, opts Options) *Actor[S, E, C] {
	if opts.MailboxSize <= 0 {
		opts.MailboxSize = 100
	}
	a := &Actor[S, E, C]{
		machine:     m,
		context:     initialContext,
		active:      make(map[S]bool),
		history:     make(map[S]S),
		invocations: make(map[S]context.CancelFunc),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan E, opts.MailboxSize),
	}
	
	// Resolve all initial states (handling hierarchy and defaults)
	a.enterState(m.Initial)
	
	// Handle any transient transitions that fire immediately
	a.handleAlways()
	
	go a.loop()
	return a
}

// Hydrate restores an Actor instance from a previously captured Snapshot.
// It restarts any services (invocations/timers) associated with the active states
// without re-executing state entry actions.
func Hydrate[S ~string, E ~string, C any](m *Machine[S, E, C], snapshot Snapshot[S, C]) *Actor[S, E, C] {
	active := make(map[S]bool)
	for _, sID := range snapshot.Active {
		active[sID] = true
	}

	a := &Actor[S, E, C]{
		machine:     m,
		context:     snapshot.Context,
		active:      active,
		history:     snapshot.History,
		invocations: make(map[S]context.CancelFunc),
		timers:      make(map[S][]*time.Timer),
		mailbox:     make(chan E, 100),
	}

	// Restart background services for all active states
	for sID := range active {
		a.restartServices(sID)
	}

	go a.loop()
	return a
}

// Stop stops the actor, cancels all running services/timers, and closes the mailbox.
func (a *Actor[S, E, C]) Stop() {
	a.stopOnce.Do(func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		
		// Cancel all invocations
		if a.invocations != nil {
			for _, cancel := range a.invocations {
				cancel()
			}
			a.invocations = make(map[S]context.CancelFunc)
		}

		// Stop all timers
		if a.timers != nil {
			for _, timers := range a.timers {
				for _, t := range timers {
					t.Stop()
				}
			}
			a.timers = make(map[S][]*time.Timer)
		}

		if a.mailbox != nil {
			close(a.mailbox)
		}
	})
}

// Context returns a thread-safe copy of the current context.
// If C implements Cloner[C], it returns the result of c.Clone().
func (a *Actor[S, E, C]) Context() C {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if cloner, ok := any(a.context).(Cloner[C]); ok {
		return cloner.Clone()
	}
	return a.context
}

// restartServices triggers the background tasks (invokes/timers) for a state.
func (a *Actor[S, E, C]) restartServices(id S) {
	stateDef := a.machine.States[id]
	if stateDef == nil { return }

	// Start or restart invoked services
	if stateDef.Invoke != nil {
		ctx, cancel := context.WithCancel(context.Background())
		a.invocations[id] = cancel
		go func(sID S, invoke *InvokeDef[S, E, C], c C) {
			err := invoke.Src(ctx, c)
			if ctx.Err() != nil {
				return // cancelled by state exit
			}
			target := invoke.OnDone
			if err != nil {
				target = invoke.OnError
			}
			if target != "" {
				a.executeInternalTransition(sID, target)
			}
		}(id, stateDef.Invoke, a.context)
	}

	// Start or restart delayed transitions
	for _, t := range stateDef.Delayed {
		trans := t // capture for closure
		timer := time.AfterFunc(t.After, func() {
			a.executeInternalTransition(id, trans.Target)
		})
		a.timers[id] = append(a.timers[id], timer)
	}
}

// Snapshot captures the current status of the Actor, including its active states,
// history data, and context. The returned struct is suitable for JSON serialization.
func (a *Actor[S, E, C]) Snapshot() Snapshot[S, C] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	active := make([]S, 0, len(a.active))
	for sID := range a.active {
		active = append(active, sID)
	}

	history := make(map[S]S)
	for k, v := range a.history {
		history[k] = v
	}

	ctx := a.context
	if cloner, ok := any(a.context).(Cloner[C]); ok {
		ctx = cloner.Clone()
	}

	return Snapshot[S, C]{
		Active:  active,
		History: history,
		Context: ctx,
	}
}

// enterState handles the full entry logic for a state, including its children.
func (a *Actor[S, E, C]) enterState(id S) {
	a.enterSingleState(id)
	a.enterChildrenWithHistory(id, false)
}

// States returns the current list of all active states, ordered from root to leaf.
func (a *Actor[S, E, C]) States() []S {
	a.mu.RLock()
	defer a.mu.RUnlock()
	
	// Get states sorted by depth (deepest first)
	active := a.getSortedActiveStatesLocked()
	
	// Reverse to get root-to-leaf order
	res := make([]S, len(active))
	for i, sID := range active {
		res[len(active)-1-i] = sID
	}
	return res
}

// State returns the ID of the current deepest active leaf state.
// In the case of parallel states, it returns one of the active leaf states.
func (a *Actor[S, E, C]) State() S {
	a.mu.RLock()
	defer a.mu.RUnlock()
	active := a.getSortedActiveStatesLocked()
	if len(active) > 0 {
		return active[0] // deepest leaf
	}
	return ""
}

// getSortedActiveStatesLocked retrieves active states sorted by depth (deepest first).
// Must be called with at least a read lock.
func (a *Actor[S, E, C]) getSortedActiveStatesLocked() []S {
	res := make([]S, 0, len(a.active))
	for sID := range a.active {
		res = append(res, sID)
	}

	// Sort by depth descending (leaves first) using pre-computed depth
	for i := 0; i < len(res); i++ {
		for j := i + 1; j < len(res); j++ {
			dI := 0
			dJ := 0
			if sI, ok := a.machine.States[res[i]]; ok { dI = sI.Depth }
			if sJ, ok := a.machine.States[res[j]]; ok { dJ = sJ.Depth }
			
			if dI < dJ {
				res[i], res[j] = res[j], res[i]
			}
		}
	}
	return res
}

// Send queues an event in the actor's mailbox for sequential processing.
// This method is non-blocking.
func (a *Actor[S, E, C]) Send(event E) {
	// If the mailbox is closed, this will panic. 
	// Ideally we check context or stop channel, but for channel send it's idiomatic 
	// to let the user ensure they don't send to a stopped actor or use a recover.
	// For simplicity in this library, we assume the user manages lifecycle or we could add a check.
	// Adding a non-blocking check on a closed channel is hard. 
	// We'll trust the user or the actor loop.
	defer func() {
		// Recover if sending to closed channel
		recover() 
	}()
	a.mailbox <- event
}

// loop is the main event loop running in a background goroutine.
func (a *Actor[S, E, C]) loop() {
	for event := range a.mailbox {
		a.handleEvent(event)
	}
}

func (a *Actor[S, E, C]) handleEvent(event E) {
	a.mu.Lock()
	defer a.mu.Unlock()

	activeStates := a.getSortedActiveStatesLocked()
	// Bubble up: check transitions from deepest state to root
	for _, sID := range activeStates {
		stateDef := a.machine.States[sID]
		if transitions, ok := stateDef.Transitions[event]; ok {
			for _, t := range transitions {
				if t.Guard == nil || t.Guard(a.context) {
					a.executeTransition(sID, t)
					// Check for transient transitions after the event transition
					a.handleAlwaysInternal()
					return
				}
			}
		}
	}
}

// executeTransition performs the state transition logic, including LCA resolution,
// entry/exit actions, and context updates.
func (a *Actor[S, E, C]) executeTransition(sourceID S, t *TransitionDef[S, E, C]) {
	// 1. Handle internal transitions (no target)
	if t.Target == "" {
		if t.Action != nil {
			a.context = t.Action(a.context)
		}
		return
	}

	targetState, ok := a.machine.States[t.Target]
	if !ok { return }
	sourceState, ok := a.machine.States[sourceID]
	if !ok { return }

	targetPath := targetState.Path
	sourcePath := sourceState.Path
	
	// Find Lowest Common Ancestor (LCA) to determine which states to exit/enter
	lcaID := S("")
	for i := 0; i < len(sourcePath) && i < len(targetPath); i++ {
		if sourcePath[i] == targetPath[i] {
			lcaID = sourcePath[i]
		} else {
			break
		}
	}

	// 2. Exit states from deepest active up to but not including LCA
	toExit := []S{}
	sortedActive := a.getSortedActiveStatesLocked()
	
	// FIX: Self-transition behavior.
	isSelfTransition := sourceID == t.Target
	
	for _, sID := range sortedActive {
		if isSelfTransition {
			if sID == sourceID || a.isDescendant(sID, sourceID) {
				toExit = append(toExit, sID)
			}
		} else {
			// Standard LCA logic: Exit if descendant of LCA and NOT the LCA itself
			if sID != lcaID && a.isDescendant(sID, lcaID) {
				toExit = append(toExit, sID)
			}
		}
	}

	for _, sID := range toExit {
		stateDef := a.machine.States[sID]
		if stateDef != nil {
			for _, action := range stateDef.Exit {
				a.context = action(a.context)
			}
			if cancel, ok := a.invocations[sID]; ok {
				cancel()
				delete(a.invocations, sID)
			}
			if ts, ok := a.timers[sID]; ok {
				for _, timer := range ts {
					timer.Stop()
				}
				delete(a.timers, sID)
			}
			if stateDef.Parent != "" {
				a.history[stateDef.Parent] = sID
			}
		}
		delete(a.active, sID)
	}

	// 3. Execute transition action (Assign)
	if t.Action != nil {
		a.context = t.Action(a.context)
	}

	// 4. Enter states from LCA (exclusive) down to target
	if isSelfTransition {
		a.enterSingleState(t.Target)
		a.enterChildrenWithHistory(t.Target, false)
	} else {
		lcaDepth := -1
		if lcaID != "" {
			for i, id := range targetPath {
				if id == lcaID {
					lcaDepth = i
					break
				}
			}
		}

		for i := lcaDepth + 1; i < len(targetPath); i++ {
			a.enterSingleState(targetPath[i])
		}
		
		// 5. Resolve children of the target state
		if len(targetPath) > 0 {
			a.enterChildrenWithHistory(targetPath[len(targetPath)-1], false)
		}
	}
}

// enterSingleState marks a state as active and runs its entry actions.
func (a *Actor[S, E, C]) enterSingleState(id S) {
	a.active[id] = true
	stateDef := a.machine.States[id]
	if stateDef == nil { return }

	for _, action := range stateDef.Entry {
		a.context = action(a.context)
	}

	a.restartServices(id)
}

// executeInternalTransition handles transitions triggered by services (invokes/timers).
func (a *Actor[S, E, C]) executeInternalTransition(sourceID S, targetID S) {
	a.mu.Lock()
	defer a.mu.Unlock()
	
	// Verify the source state is still active before transitioning
	if !a.active[sourceID] {
		return
	}

	t := &TransitionDef[S, E, C]{Target: targetID}
	a.executeTransition(sourceID, t)
	a.handleAlwaysInternal()
}

// enterChildrenWithHistory resolves the child states to enter, respecting history and parallel types.
func (a *Actor[S, E, C]) enterChildrenWithHistory(id S, deepHistory bool) {
	stateDef := a.machine.States[id]
	if stateDef == nil { return }

	if stateDef.Type == Parallel {
		// Parallel regions: enter all regions
		for childID := range stateDef.States {
			a.enterSingleState(childID)
			a.enterChildrenWithHistory(childID, deepHistory)
		}
	} else {
		// Compound regions: determine which child to enter
		targetID := stateDef.Initial
		useDeepHistory := deepHistory || stateDef.History == Deep
		
		if (stateDef.History == Shallow || stateDef.History == Deep) && a.history[id] != "" {
			targetID = a.history[id]
		} else if deepHistory && a.history[id] != "" {
			targetID = a.history[id]
		}

		if targetID != "" {
			a.enterSingleState(targetID)
			a.enterChildrenWithHistory(targetID, useDeepHistory)
		}
	}
}

// getPathToRoot returns the pre-computed slice of ancestor IDs for a state.
func (a *Actor[S, E, C]) getPathToRoot(id S) []S {
	if s, ok := a.machine.States[id]; ok {
		return s.Path
	}
	return []S{}
}

// isDescendant returns true if childID is a descendant of parentID.
func (a *Actor[S, E, C]) isDescendant(childID, parentID S) bool {
	if parentID == "" { return true }
	curr := childID
	for curr != "" {
		if curr == parentID { return true }
		stateDef := a.machine.States[curr]
		if stateDef != nil {
			curr = stateDef.Parent
		} else {
			break
		}
	}
	return false
}

// handleAlways acquires a lock and checks for transient transitions.
func (a *Actor[S, E, C]) handleAlways() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handleAlwaysInternal()
}

// handleAlwaysInternal checks active states for Always transitions.
// Must be called with a write lock.
// It includes a circuit breaker to prevent infinite loops.
func (a *Actor[S, E, C]) handleAlwaysInternal() {
	const maxIterations = 100
	iterations := 0

	for {
		iterations++
		if iterations > maxIterations {
			// Panic or log error? For a library, panic might be too harsh, but infinite loop is fatal.
			// Ideally we return an error, but this is an internal void method.
			// We will break to save the process.
			fmt.Printf("gstate: infinite loop detected in Always transitions (max %d)\n", maxIterations)
			break
		}

		transitioned := false
		sortedActive := a.getSortedActiveStatesLocked()
		
		for _, sID := range sortedActive {
			stateDef := a.machine.States[sID]
			for _, t := range stateDef.Always {
				if t.Guard == nil || t.Guard(a.context) {
					a.executeTransition(sID, t)
					transitioned = true
					break // restart loop to re-evaluate active states
				}
			}
			if transitioned {
				break
			}
		}

		if !transitioned {
			break
		}
	}
}
