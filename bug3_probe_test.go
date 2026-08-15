package main

import (
	"sync"
	"testing"
)

// TestProbe_ConcurrentGenerate verifies that concurrent Generate calls on the
// same machine produce unique, non-overlapping IDs without data races.
func TestProbe_ConcurrentGenerate(t *testing.T) {
	s := NewService()
	if _, err := s.RegisterMachine(1); err != nil {
		t.Fatalf("register: %v", err)
	}

	const goroutines = 16
	const perGoroutine = 500
	results := make([][]uint64, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(idx int) {
			defer wg.Done()
			ids, err := s.Generate(1, perGoroutine)
			if err != nil {
				t.Errorf("goroutine %d: %v", idx, err)
				return
			}
			results[idx] = ids
		}(g)
	}
	wg.Wait()

	seen := make(map[uint64]bool)
	for g, ids := range results {
		for _, id := range ids {
			if id == 0 {
				continue // skip zero-padding from other bugs
			}
			if seen[id] {
				t.Fatalf("duplicate id from goroutine %d", g)
			}
			seen[id] = true
		}
	}
	// With 16 goroutines * 500 IDs = 8000 expected unique non-zero IDs.
	// Due to the slice bug, each call returns 2*perGoroutine elements, but only
	// perGoroutine are non-zero. So we expect 8000 unique non-zero IDs if there
	// is no concurrency bug.
	total := goroutines * perGoroutine
	if len(seen) != total {
		t.Fatalf("unique non-zero ids = %d, want %d", len(seen), total)
	}
}
