package webhookd

import (
	"sync"
	"time"
)

// Defaults sized for GitHub's redelivery behavior: generous enough to cover
// GitHub's redelivery window, bounded so a delivery storm cannot grow the
// cache unbounded.
const (
	defaultDedupTTL      = 30 * time.Minute
	defaultDedupCapacity = 8192
)

// DeliveryDedup is a bounded, TTL'd cache of recently-seen (provider,
// deliveryID) pairs. Safe for concurrent use.
type DeliveryDedup struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	entries  map[string]time.Time // key -> expiry
}

// NewDeliveryDedup constructs a DeliveryDedup. A zero or negative ttl or
// capacity falls back to the package defaults.
func NewDeliveryDedup(ttl time.Duration, capacity int) *DeliveryDedup {
	if ttl <= 0 {
		ttl = defaultDedupTTL
	}
	if capacity <= 0 {
		capacity = defaultDedupCapacity
	}
	return &DeliveryDedup{
		ttl:      ttl,
		capacity: capacity,
		now:      time.Now,
		entries:  make(map[string]time.Time),
	}
}

func dedupKey(provider, deliveryID string) string {
	// NUL cannot appear in either operand (provider is a validated route
	// segment, deliveryID is a header value with no interior NULs), so this
	// concatenation cannot collide across the pair boundary.
	return provider + "\x00" + deliveryID
}

// Seen reports whether (provider, deliveryID) was already claimed within
// the TTL window. If not, it atomically claims the pair (subsequent calls
// within the TTL return true) and returns false. Callers that fail to
// finish processing a delivery after a false (newly-claimed) result should
// call Forget so a legitimate sender retry is not silently swallowed.
func (d *DeliveryDedup) Seen(provider, deliveryID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()
	d.evictExpiredLocked(now)

	key := dedupKey(provider, deliveryID)
	if exp, ok := d.entries[key]; ok && now.Before(exp) {
		return true
	}
	if len(d.entries) >= d.capacity {
		d.evictOldestLocked()
	}
	d.entries[key] = now.Add(d.ttl)
	return false
}

// Forget un-claims a delivery, allowing a subsequent redelivery with the
// same ID to be processed again. Intended for the case where a delivery was
// claimed via Seen but genuinely failed to process (as opposed to being
// rejected before the dedup check, e.g. bad signature).
func (d *DeliveryDedup) Forget(provider, deliveryID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.entries, dedupKey(provider, deliveryID))
}

// clear removes every entry. Test seam.
func (d *DeliveryDedup) clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = make(map[string]time.Time)
}

// len reports the current entry count, expired entries included. Test seam.
func (d *DeliveryDedup) len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.entries)
}

func (d *DeliveryDedup) evictExpiredLocked(now time.Time) {
	for k, exp := range d.entries {
		if !now.Before(exp) {
			delete(d.entries, k)
		}
	}
}

// evictOldestLocked drops the single entry with the earliest expiry, used
// when the cache is at capacity and every entry is still live. O(n) is
// acceptable here: eviction only runs once every `capacity` claims land in
// the same TTL window, which is the pathological case, not the common one.
func (d *DeliveryDedup) evictOldestLocked() {
	var oldestKey string
	var oldestExp time.Time
	first := true
	for k, exp := range d.entries {
		if first || exp.Before(oldestExp) {
			oldestKey, oldestExp = k, exp
			first = false
		}
	}
	if !first {
		delete(d.entries, oldestKey)
	}
}
