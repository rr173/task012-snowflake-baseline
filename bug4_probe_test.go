package main

import (
	"testing"
)

// TestProbe_RemoveMachineListConsistency verifies that after removing a machine,
// ListMachines no longer includes it.
func TestProbe_RemoveMachineListConsistency(t *testing.T) {
	s := NewService()
	for _, m := range []int64{5, 10, 15} {
		if _, err := s.RegisterMachine(m); err != nil {
			t.Fatalf("register %d: %v", m, err)
		}
	}

	// Remove machine 10.
	if err := s.RemoveMachine(10); err != nil {
		t.Fatalf("remove 10: %v", err)
	}

	// ListMachines should only contain 5 and 15.
	machines := s.ListMachines()
	for _, m := range machines {
		if m.MachineID == 10 {
			t.Fatalf("ListMachines still contains removed machine 10")
		}
	}
	if len(machines) != 2 {
		t.Fatalf("len(machines) = %d, want 2", len(machines))
	}
}
