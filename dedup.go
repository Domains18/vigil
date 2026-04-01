package vigil

import "time"

// dedupEntry tracks occurrences of a single error fingerprint within a window.
type dedupEntry struct {
	fingerprint string
	count       int
	firstSeen   time.Time
	lastSeen    time.Time
	sample      *Event
	everSeen    bool // true after this fingerprint has appeared in a completed window
}

// dedupMap is the in-memory deduplication store, owned by the event loop goroutine.
// No mutex is needed — only the event loop goroutine reads and writes it.
type dedupMap struct {
	entries map[string]*dedupEntry
	ttl     time.Duration
}

func newDedupMap(ttl time.Duration) *dedupMap {
	return &dedupMap{
		entries: make(map[string]*dedupEntry),
		ttl:     ttl,
	}
}

// isNew returns true if this fingerprint has never been recorded before.
// Used to decide whether to send an ImmediateOnFirst alert.
func (d *dedupMap) isNew(fp string) bool {
	_, ok := d.entries[fp]
	return !ok
}

// record registers a new event occurrence.
func (d *dedupMap) record(event *Event) {
	fp := event.Fingerprint
	entry, ok := d.entries[fp]
	if !ok {
		entry = &dedupEntry{fingerprint: fp}
		d.entries[fp] = entry
	}
	entry.count++
	entry.lastSeen = event.Timestamp
	if entry.firstSeen.IsZero() {
		entry.firstSeen = event.Timestamp
	}
	if entry.sample == nil {
		entry.sample = event
	}
}

// drainGroups returns ErrorGroups for all fingerprints that have counts > 0,
// resets per-window counters, and marks all entries as everSeen.
func (d *dedupMap) drainGroups() []*ErrorGroup {
	groups := make([]*ErrorGroup, 0, len(d.entries))
	for fp, entry := range d.entries {
		if entry.count == 0 {
			continue
		}
		groups = append(groups, &ErrorGroup{
			Fingerprint: fp,
			Count:       entry.count,
			IsNew:       !entry.everSeen,
			FirstSeen:   entry.firstSeen,
			LastSeen:    entry.lastSeen,
			Sample:      entry.sample,
		})
		// Reset window counters but keep the entry for TTL tracking.
		entry.count = 0
		entry.everSeen = true
		entry.sample = nil
		entry.firstSeen = time.Time{}
	}
	return groups
}

// evict removes entries whose lastSeen is older than the TTL.
// Called on every digest tick to bound map size.
func (d *dedupMap) evict(now time.Time) {
	for fp, entry := range d.entries {
		if now.Sub(entry.lastSeen) > d.ttl {
			delete(d.entries, fp)
		}
	}
}
