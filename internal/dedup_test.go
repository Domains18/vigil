package core

import (
	"testing"
	"time"
)

func TestDedupRecord(t *testing.T) {
	d := newDedupMap(time.Hour)
	now := time.Now()

	e := &Event{Fingerprint: "abc123", Timestamp: now}
	d.record(e)
	d.record(e)
	d.record(e)

	entry := d.entries["abc123"]
	if entry == nil {
		t.Fatal("entry should exist after record")
	}
	if entry.count != 3 {
		t.Errorf("count: got %d, want 3", entry.count)
	}
	if entry.sample == nil {
		t.Error("sample should be set")
	}
}

func TestDedupIsNew(t *testing.T) {
	d := newDedupMap(time.Hour)
	now := time.Now()

	if !d.isNew("fp1") {
		t.Error("isNew should return true for unseen fingerprint")
	}

	d.record(&Event{Fingerprint: "fp1", Timestamp: now})

	if d.isNew("fp1") {
		t.Error("isNew should return false after record")
	}
}

func TestDedupDrainGroups(t *testing.T) {
	d := newDedupMap(time.Hour)
	now := time.Now()

	d.record(&Event{Fingerprint: "fp1", Timestamp: now, Error: "err1"})
	d.record(&Event{Fingerprint: "fp1", Timestamp: now.Add(time.Second), Error: "err1"})
	d.record(&Event{Fingerprint: "fp2", Timestamp: now, Error: "err2"})

	groups := d.drainGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	countByFP := map[string]int{}
	isNewByFP := map[string]bool{}
	for _, g := range groups {
		countByFP[g.Fingerprint] = g.Count
		isNewByFP[g.Fingerprint] = g.IsNew
	}

	if countByFP["fp1"] != 2 {
		t.Errorf("fp1 count: got %d, want 2", countByFP["fp1"])
	}
	if countByFP["fp2"] != 1 {
		t.Errorf("fp2 count: got %d, want 1", countByFP["fp2"])
	}
	if !isNewByFP["fp1"] || !isNewByFP["fp2"] {
		t.Error("first drain: all groups should be IsNew=true")
	}

	d.record(&Event{Fingerprint: "fp1", Timestamp: now.Add(2 * time.Second)})
	groups2 := d.drainGroups()
	if len(groups2) != 1 {
		t.Fatalf("second drain: expected 1 group, got %d", len(groups2))
	}
	if groups2[0].IsNew {
		t.Error("second drain: fp1 should be IsNew=false (recurring)")
	}
}

func TestDedupEviction(t *testing.T) {
	ttl := 100 * time.Millisecond
	d := newDedupMap(ttl)

	past := time.Now().Add(-200 * time.Millisecond)
	d.record(&Event{Fingerprint: "old", Timestamp: past})
	d.entries["old"].lastSeen = past

	d.record(&Event{Fingerprint: "new", Timestamp: time.Now()})

	d.evict(time.Now())

	if _, ok := d.entries["old"]; ok {
		t.Error("old entry should have been evicted")
	}
	if _, ok := d.entries["new"]; !ok {
		t.Error("new entry should not have been evicted")
	}
}

func TestDedupEvictedEntryTreatedAsNew(t *testing.T) {
	ttl := 50 * time.Millisecond
	d := newDedupMap(ttl)

	past := time.Now().Add(-200 * time.Millisecond)
	d.record(&Event{Fingerprint: "fp", Timestamp: past})
	d.entries["fp"].lastSeen = past
	d.entries["fp"].everSeen = true

	d.evict(time.Now())

	if !d.isNew("fp") {
		t.Error("evicted fingerprint should be treated as new")
	}
}
