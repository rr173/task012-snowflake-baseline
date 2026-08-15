package main

import (
	"strconv"
	"testing"
)

// TestProbe_InspectValidID verifies that Inspect accepts a legitimately composed
// ID (sign bit = 0) and returns its decomposed fields correctly.
func TestProbe_InspectValidID(t *testing.T) {
	s := NewService()
	// Manually compose a valid ID: timestamp=5000000, machine=42, seq=0.
	// This is a perfectly valid snowflake ID with sign bit = 0.
	id := compose(5_000_000, 42, 0)
	if id>>signBit != 0 {
		t.Fatalf("test setup: composed id has sign bit set")
	}
	idStr := strconv.FormatUint(id, 10)
	ins, err := s.Inspect(idStr)
	if err != nil {
		t.Fatalf("Inspect(%s) returned error: %v", idStr, err)
	}
	if ins.MachineID != 42 {
		t.Fatalf("machineID = %d, want 42", ins.MachineID)
	}
	if ins.Timestamp != 5_000_000 {
		t.Fatalf("timestamp = %d, want 5000000", ins.Timestamp)
	}
}
