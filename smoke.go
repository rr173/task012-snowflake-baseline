package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
)

// fakeClock is a controllable millisecond clock shared by smoke tests and unit
// tests that need deterministic control over the generator's time source.
type fakeClock struct {
	mu  sync.Mutex
	now int64
}

func (f *fakeClock) set(v int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = v
}

func (f *fakeClock) get() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func machineIDs(ms []*Machine) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.MachineID)
	}
	return out
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runSmokeTest exercises the core service and the HTTP layer end-to-end without
// any external dependency. It returns a non-nil error (causing exit 1) on the
// first failed assertion.
func runSmokeTest() error {
	if err := smokeCore(); err != nil {
		return fmt.Errorf("core: %w", err)
	}
	if err := smokeHTTP(); err != nil {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

func smokeCore() error {
	// Scenario 1: register a machine and generate a batch of unique,
	// strictly-increasing IDs.
	s := NewService()
	if _, err := s.RegisterMachine(1); err != nil {
		return fmt.Errorf("register 1: %w", err)
	}
	ids, err := s.Generate(1, 500)
	if err != nil {
		return fmt.Errorf("generate 500: %w", err)
	}
	if len(ids) != 500 {
		return fmt.Errorf("got %d ids, want 500", len(ids))
	}
	seen := make(map[uint64]struct{}, len(ids))
	for i, id := range ids {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("duplicate id %d", id)
		}
		seen[id] = struct{}{}
		if i > 0 && id <= ids[i-1] {
			return fmt.Errorf("non-monotonic id %d after %d", id, ids[i-1])
		}
	}

	// Scenario 2: decompose a generated id and verify the fields round-trip and
	// match the owning machine.
	probe := ids[123]
	ts, machineID, seq := decompose(probe)
	if machineID != 1 {
		return fmt.Errorf("decompose machineID = %d, want 1", machineID)
	}
	if seq > maxSequence {
		return fmt.Errorf("decompose seq = %d exceeds %d", seq, maxSequence)
	}
	if got := compose(ts, machineID, seq); got != probe {
		return fmt.Errorf("round-trip failed: %d != %d", got, probe)
	}
	ins, err := s.Inspect(strconv.FormatUint(probe, 10))
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if ins.MachineID != 1 || ins.ID != strconv.FormatUint(probe, 10) {
		return fmt.Errorf("inspect mismatch: %+v", ins)
	}

	// Scenario 3: two machines in the same millisecond produce disjoint IDs.
	fc := &fakeClock{}
	s2 := newServiceWithClock(fc.get)
	for _, m := range []int64{10, 11} {
		if _, err := s2.RegisterMachine(m); err != nil {
			return fmt.Errorf("register %d: %w", m, err)
		}
	}
	fc.set(EpochMs + 8_000_000)
	batch := map[uint64]int64{}
	for i := 0; i < 100; i++ {
		for _, m := range []int64{10, 11} {
			got, err := s2.Generate(m, 1)
			if err != nil {
				return fmt.Errorf("generate from %d: %w", m, err)
			}
			id := got[0]
			if prev, dup := batch[id]; dup {
				return fmt.Errorf("cross-machine duplicate %d (machines %d and %d)", id, prev, m)
			}
			batch[id] = m
		}
	}

	// Scenario 4: validation errors.
	s3 := NewService()
	if _, err := s3.RegisterMachine(-1); err == nil {
		return errors.New("negative machineID should be rejected")
	}
	if _, err := s3.RegisterMachine(1024); err == nil {
		return errors.New("machineID > 1023 should be rejected")
	}
	if _, err := s3.RegisterMachine(0); err != nil {
		return fmt.Errorf("register 0: %w", err)
	}
	if _, err := s3.RegisterMachine(0); err == nil {
		return errors.New("duplicate machineID should be rejected")
	}
	if _, err := s3.Generate(999, 1); err == nil {
		return errors.New("generating from unregistered machine should fail")
	}
	if _, err := s3.Generate(0, 0); err == nil {
		return errors.New("zero count should be rejected")
	}
	if _, err := s3.Generate(0, MaxBatch+1); err == nil {
		return errors.New("oversized count should be rejected")
	}
	if _, err := s3.Inspect("not-a-number"); err == nil {
		return errors.New("non-numeric id should be rejected")
	}
	if _, err := s3.Inspect("9223372036854775808"); err == nil {
		return errors.New("id with sign bit set should be rejected")
	}
	if err := s3.RemoveMachine(999); err == nil {
		return errors.New("removing an unregistered machine should fail")
	}

	// Scenario 5: list follows registration order and survives removal.
	s4 := NewService()
	for _, m := range []int64{3, 1, 2} {
		if _, err := s4.RegisterMachine(m); err != nil {
			return fmt.Errorf("register %d: %w", m, err)
		}
	}
	if got := machineIDs(s4.ListMachines()); !equalInt64(got, []int64{3, 1, 2}) {
		return fmt.Errorf("list order = %v, want [3 1 2]", got)
	}
	if err := s4.RemoveMachine(1); err != nil {
		return fmt.Errorf("remove 1: %w", err)
	}
	if got := machineIDs(s4.ListMachines()); !equalInt64(got, []int64{3, 2}) {
		return fmt.Errorf("list order after remove = %v, want [3 2]", got)
	}

	return nil
}

func smokeHTTP() error {
	srv := NewService()
	ts := httptest.NewServer(buildMux(srv))
	defer ts.Close()

	// healthz
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}

	// register a machine
	resp, err = http.Post(ts.URL+"/machines", "application/json", bytes.NewBufferString(`{"machineID":1}`))
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("register status = %d, want 201", resp.StatusCode)
	}

	// generate ids
	resp, err = http.Post(ts.URL+"/machines/1/ids", "application/json", bytes.NewBufferString(`{"count":5}`))
	if err != nil {
		return fmt.Errorf("generate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("generate status = %d, want 200", resp.StatusCode)
	}
	var res struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("decode ids: %w", err)
	}
	if len(res.IDs) != 5 {
		return fmt.Errorf("got %d ids, want 5", len(res.IDs))
	}

	// inspect the first id
	resp, err = http.Get(ts.URL + "/ids/" + res.IDs[0])
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inspect status = %d, want 200", resp.StatusCode)
	}
	var ins Inspection
	if err := json.NewDecoder(resp.Body).Decode(&ins); err != nil {
		return fmt.Errorf("decode inspection: %w", err)
	}
	if ins.ID != res.IDs[0] || ins.MachineID != 1 {
		return fmt.Errorf("inspection mismatch: %+v", ins)
	}

	// list machines
	resp, err = http.Get(ts.URL + "/machines")
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("list status = %d, want 200", resp.StatusCode)
	}
	var list struct {
		Machines []*Machine `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return fmt.Errorf("decode list: %w", err)
	}
	if len(list.Machines) != 1 || list.Machines[0].MachineID != 1 {
		return fmt.Errorf("list mismatch: %+v", list)
	}

	// invalid JSON
	resp, err = http.Post(ts.URL+"/machines", "application/json", bytes.NewBufferString(`{bad`))
	if err != nil {
		return fmt.Errorf("post bad json: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("bad json status = %d, want 400", resp.StatusCode)
	}

	// missing required field
	resp, err = http.Post(ts.URL+"/machines", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		return fmt.Errorf("post empty: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("missing field status = %d, want 400", resp.StatusCode)
	}

	// out-of-range machineID
	resp, err = http.Post(ts.URL+"/machines", "application/json", bytes.NewBufferString(`{"machineID":5000}`))
	if err != nil {
		return fmt.Errorf("post oor: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("out-of-range status = %d, want 400", resp.StatusCode)
	}

	// duplicate machineID -> 409
	resp, err = http.Post(ts.URL+"/machines", "application/json", bytes.NewBufferString(`{"machineID":1}`))
	if err != nil {
		return fmt.Errorf("post dup: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		return fmt.Errorf("duplicate status = %d, want 409", resp.StatusCode)
	}

	// generate from unregistered machine -> 404
	resp, err = http.Post(ts.URL+"/machines/42/ids", "application/json", bytes.NewBufferString(`{"count":1}`))
	if err != nil {
		return fmt.Errorf("post unregistered gen: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("unregistered generate status = %d, want 404", resp.StatusCode)
	}

	// inspect invalid id -> 400
	resp, err = http.Get(ts.URL + "/ids/abc")
	if err != nil {
		return fmt.Errorf("get bad id: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("bad id status = %d, want 400", resp.StatusCode)
	}

	// delete a missing machine -> 404
	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/machines/99", nil)
	if err != nil {
		return fmt.Errorf("new delete: %w", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete missing: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("delete missing status = %d, want 404", resp.StatusCode)
	}

	// delete an existing machine -> 200, then generating from it -> 404
	req, err = http.NewRequest(http.MethodDelete, ts.URL+"/machines/1", nil)
	if err != nil {
		return fmt.Errorf("new delete 1: %w", err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete 1: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete 1 status = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Post(ts.URL+"/machines/1/ids", "application/json", bytes.NewBufferString(`{"count":1}`))
	if err != nil {
		return fmt.Errorf("post after delete: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("generate after delete status = %d, want 404", resp.StatusCode)
	}

	return nil
}
