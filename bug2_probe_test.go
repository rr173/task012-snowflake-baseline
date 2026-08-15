package main

import (
	"testing"
)

// TestProbe_GenerateCountMatch verifies that Generate(machineID, n) returns
// exactly n IDs and none of them are zero.
func TestProbe_GenerateCountMatch(t *testing.T) {
	s := NewService()
	if _, err := s.RegisterMachine(1); err != nil {
		t.Fatalf("register: %v", err)
	}
	const want = 10
	ids, err := s.Generate(1, want)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(ids) != want {
		t.Fatalf("len(ids) = %d, want %d", len(ids), want)
	}
	for i, id := range ids {
		if id == 0 {
			t.Fatalf("ids[%d] is zero; all generated IDs must be non-zero", i)
		}
	}
}
