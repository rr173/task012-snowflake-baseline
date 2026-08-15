package main

import (
	"testing"
)

// TestProbe_TimestampDecomposeRoundTrip verifies that a generated ID decomposes
// to a timestamp that equals (genTime - EpochMs), confirming the epoch offset is
// correctly applied during generation.
func TestProbe_TimestampDecomposeRoundTrip(t *testing.T) {
	fc := &fakeClock{}
	s := newServiceWithClock(fc.get)
	if _, err := s.RegisterMachine(7); err != nil {
		t.Fatalf("register: %v", err)
	}

	// Generate at a known absolute time: EpochMs + 1_234_567 ms.
	genTime := EpochMs + 1_234_567
	fc.set(genTime)

	// Call Generate with count=1. Even if the result slice has extra elements
	// due to bugs, we inspect the non-zero element.
	ids, err := s.Generate(7, 1)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Find the first non-zero id in the slice.
	var target uint64
	for _, id := range ids {
		if id != 0 {
			target = id
			break
		}
	}
	if target == 0 {
		t.Fatalf("no non-zero ID generated")
	}

	// Decompose and verify the timestamp field equals 1_234_567
	// (the delta from the custom epoch, not the absolute Unix millis).
	ts, machineID, _ := decompose(target)
	if machineID != 7 {
		t.Fatalf("machineID = %d, want 7", machineID)
	}
	wantTs := uint64(1_234_567)
	if ts != wantTs {
		t.Fatalf("decomposed timestamp = %d, want %d (delta from epoch)", ts, wantTs)
	}
}
