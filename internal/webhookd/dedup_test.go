package webhookd

import (
	"testing"
	"time"
)

func TestDeliveryDedupFirstSeenClaimsAndReturnsFalse(t *testing.T) {
	d := NewDeliveryDedup(time.Minute, 10)
	if d.Seen("github", "abc") {
		t.Fatal("first Seen call returned true, want false (newly claimed)")
	}
	if got, want := d.len(), 1; got != want {
		t.Fatalf("len() = %d, want %d", got, want)
	}
}

func TestDeliveryDedupRepeatWithinTTLReturnsTrue(t *testing.T) {
	d := NewDeliveryDedup(time.Minute, 10)
	if d.Seen("github", "abc") {
		t.Fatal("first Seen call returned true, want false")
	}
	if !d.Seen("github", "abc") {
		t.Fatal("second Seen call returned false, want true (duplicate)")
	}
}

func TestDeliveryDedupDistinguishesProvider(t *testing.T) {
	d := NewDeliveryDedup(time.Minute, 10)
	if d.Seen("github", "abc") {
		t.Fatal("first Seen call for github returned true, want false")
	}
	if d.Seen("vercel", "abc") {
		t.Fatal("first Seen call for vercel with same delivery id returned true, want false")
	}
}

func TestDeliveryDedupForgetUnclaims(t *testing.T) {
	d := NewDeliveryDedup(time.Minute, 10)
	if d.Seen("github", "abc") {
		t.Fatal("first Seen call returned true, want false")
	}
	d.Forget("github", "abc")
	if d.Seen("github", "abc") {
		t.Fatal("Seen after Forget returned true, want false (unclaimed)")
	}
}

func TestDeliveryDedupTTLExpiry(t *testing.T) {
	now := time.Now()
	d := NewDeliveryDedup(time.Minute, 10)
	d.now = func() time.Time { return now }

	if d.Seen("github", "abc") {
		t.Fatal("first Seen call returned true, want false")
	}

	now = now.Add(30 * time.Second)
	if !d.Seen("github", "abc") {
		t.Fatal("Seen before TTL expiry returned false, want true")
	}

	now = now.Add(31 * time.Second) // total 61s, past the 60s TTL
	if d.Seen("github", "abc") {
		t.Fatal("Seen after TTL expiry returned true, want false (should have expired and re-claimed)")
	}
}

func TestDeliveryDedupCapacityEviction(t *testing.T) {
	now := time.Now()
	d := NewDeliveryDedup(time.Hour, 2)
	d.now = func() time.Time {
		t := now
		now = now.Add(time.Millisecond)
		return t
	}

	d.Seen("github", "1")
	d.Seen("github", "2")
	if got, want := d.len(), 2; got != want {
		t.Fatalf("len() = %d, want %d", got, want)
	}

	// Cache is at capacity; claiming a third entry must evict the oldest
	// (delivery "1") rather than growing unbounded.
	d.Seen("github", "3")
	if got, want := d.len(), 2; got != want {
		t.Fatalf("len() after eviction = %d, want %d", got, want)
	}
	if d.Seen("github", "1") {
		t.Fatal("evicted delivery \"1\" still reported Seen=true; eviction did not happen")
	}
}

func TestNewDeliveryDedupDefaults(t *testing.T) {
	d := NewDeliveryDedup(0, 0)
	if d.ttl != defaultDedupTTL {
		t.Fatalf("ttl = %v, want default %v", d.ttl, defaultDedupTTL)
	}
	if d.capacity != defaultDedupCapacity {
		t.Fatalf("capacity = %d, want default %d", d.capacity, defaultDedupCapacity)
	}
}

func TestDeliveryDedupClear(t *testing.T) {
	d := NewDeliveryDedup(time.Minute, 10)
	d.Seen("github", "abc")
	d.clear()
	if got, want := d.len(), 0; got != want {
		t.Fatalf("len() after clear = %d, want %d", got, want)
	}
	if d.Seen("github", "abc") {
		t.Fatal("Seen after clear returned true, want false")
	}
}
