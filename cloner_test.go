package gstate

import (
	"fmt"
	"testing"
	"time"
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
	actor := Start(machine, initial)

	actor.Send("INC")
	// wait for process using a loop that checks the value
	deadline := time.Now().Add(1 * time.Second)
	for actor.Context().Value == 0 && time.Now().Before(deadline) {
		time.Sleep(1 * time.Millisecond)
	}

	snapshot := actor.Snapshot()
	
	// Mutate actor context
	actor.Send("INC")
	deadline = time.Now().Add(1 * time.Second)
	for actor.Context().Value == 1 && time.Now().Before(deadline) {
		time.Sleep(1 * time.Millisecond)
	}

	// Snapshot should have the OLD data
	if snapshot.Context.Value != 1 {
		t.Errorf("Expected snapshot value 1, got %d", snapshot.Context.Value)
	}
	if len(snapshot.Context.Data) != 2 {
		t.Errorf("Expected snapshot data length 2, got %d", len(snapshot.Context.Data))
	}
	
	// Ensure deep copy of slice worked
	if actor.Context().Data[1] != 1 {
		t.Errorf("Expected actor data[1] to be 1")
	}
}
