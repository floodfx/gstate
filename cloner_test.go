package gstate

import (
	"context"
	"fmt"
	"testing"
)

type CloneCtx struct {
	Value int
	Data  []int
}

func (c *CloneCtx) Clone() *CloneCtx {
	newData := make([]int, len(c.Data))
	copy(newData, c.Data)
	return &CloneCtx{
		Value: c.Value,
		Data:  newData,
	}
}

func TestClonerSupport(t *testing.T) {
	machine := New[string, string, *CloneCtx]("clone").
		Initial("idle").
		State("idle", func(s *StateBuilder[string, string, *CloneCtx]) {
			s.On("INC").Assign(func(c *CloneCtx) *CloneCtx {
				c.Value++
				fmt.Printf("Action: incremented to %d\n", c.Value)
				c.Data = append(c.Data, c.Value)
				return c
			})
		}).
		Build()

	initial := &CloneCtx{Value: 0, Data: []int{0}}
	step := make(chan struct{}, 1)
	actor := Start(machine, initial, machine.WithObservers(ObserverFuncs[string, string, *CloneCtx]{TransitionFunc: func(context.Context, *TransitionEvent[string, string, *CloneCtx]) { step <- struct{}{} }}))
	defer actor.Stop()

	actor.Send("INC")
	<-step

	snapshot := actor.Snapshot()

	// Mutate actor context
	actor.Send("INC")
	<-step

	// Snapshot should have the OLD data
	if snapshot.Data.Value != 1 {
		t.Errorf("Expected snapshot value 1, got %d", snapshot.Data.Value)
	}
	if len(snapshot.Data.Data) != 2 {
		t.Errorf("Expected snapshot data length 2, got %d", len(snapshot.Data.Data))
	}

	// Ensure deep copy of slice worked
	if actor.Data().Data[1] != 1 {
		t.Errorf("Expected actor data[1] to be 1")
	}
}
