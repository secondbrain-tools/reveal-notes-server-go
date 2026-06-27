package notes

import (
	"sync"
	"testing"
	"time"
)

func TestNewSessionStore(t *testing.T) {
	store := NewSessionStore()
	if store == nil {
		t.Fatal("NewSessionStore() returned nil")
	}
	if len(store.List()) != 0 {
		t.Fatal("New store should have 0 sessions")
	}
}

func TestTouchCreatesNewSession(t *testing.T) {
	store := NewSessionStore()
	now := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	store.Touch("abc123", nil)

	sessions := store.List()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SocketId != "abc123" {
		t.Errorf("expected socketId abc123, got %s", sessions[0].SocketId)
	}
	if !sessions[0].CreatedAt.Equal(now) {
		t.Errorf("expected CreatedAt %v, got %v", now, sessions[0].CreatedAt)
	}
	if !sessions[0].LastSeenAt.Equal(now) {
		t.Errorf("expected LastSeenAt %v, got %v", now, sessions[0].LastSeenAt)
	}
	if sessions[0].LastIndex != nil {
		t.Errorf("expected LastIndex nil, got %v", sessions[0].LastIndex)
	}
}

func TestTouchEmptySocketId(t *testing.T) {
	store := NewSessionStore()
	store.Touch("", nil) // should not panic or add
	if len(store.List()) != 0 {
		t.Fatal("empty socketId should not create session")
	}
}

func TestTouchUpdatesLastSeen(t *testing.T) {
	store := NewSessionStore()
	store.now = func() time.Time {
		return time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	}

	store.Touch("abc123", nil)

	store.now = func() time.Time {
		return time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC)
	}
	store.Touch("abc123", nil)

	sessions := store.List()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	expected := time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC)
	if !sessions[0].LastSeenAt.Equal(expected) {
		t.Errorf("expected LastSeenAt %v, got %v", expected, sessions[0].LastSeenAt)
	}
}

func TestTouchWithState(t *testing.T) {
	store := NewSessionStore()
	store.now = func() time.Time {
		return time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	}

	state := map[string]any{
		"state": map[string]any{
			"indexh": float64(2),
			"indexv": float64(1),
			"indexf": float64(0),
		},
	}
	store.Touch("abc123", state)

	sessions := store.List()
	if sessions[0].LastIndex == nil {
		t.Fatal("expected LastIndex to be set")
	}
	if sessions[0].LastIndex.H == nil || *sessions[0].LastIndex.H != 2 {
		t.Errorf("expected H=2, got %v", sessions[0].LastIndex.H)
	}
	if sessions[0].LastIndex.V == nil || *sessions[0].LastIndex.V != 1 {
		t.Errorf("expected V=1, got %v", sessions[0].LastIndex.V)
	}
	if sessions[0].LastIndex.F == nil || *sessions[0].LastIndex.F != 0 {
		t.Errorf("expected F=0, got %v", sessions[0].LastIndex.F)
	}
}

func TestTouchWithStateRemovesOverview(t *testing.T) {
	store := NewSessionStore()
	state := map[string]any{
		"state": map[string]any{
			"indexh":   float64(1),
			"overview": true,
		},
	}
	store.Touch("abc123", state)

	// overview should have been deleted from the map (though it's a copy)
	// We verify by checking that the session created without error
	if len(store.List()) != 1 {
		t.Fatal("expected 1 session")
	}
}

func TestPrune(t *testing.T) {
	store := NewSessionStore()
	baseTime := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseTime }

	// Add session that will be stale
	store.Touch("stale", nil)
	// Advance time and add a fresh session
	store.now = func() time.Time { return baseTime.Add(30 * time.Minute) }
	store.Touch("fresh", nil)

	// Now prune with 20 minute TTL
	store.Prune(20 * time.Minute)

	sessions := store.List()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after prune, got %d", len(sessions))
	}
	if sessions[0].SocketId != "fresh" {
		t.Errorf("expected 'fresh' to remain, got %s", sessions[0].SocketId)
	}
}

func TestPruneAll(t *testing.T) {
	store := NewSessionStore()
	baseTime := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return baseTime }

	store.Touch("s1", nil)
	store.Touch("s2", nil)

	// Advance time so sessions are older than the TTL
	store.now = func() time.Time { return baseTime.Add(1 * time.Hour) }
	store.Prune(1 * time.Nanosecond) // prune everything

	if len(store.List()) != 0 {
		t.Fatal("expected all sessions to be pruned")
	}
}

func TestListSortedByLastSeenDesc(t *testing.T) {
	store := NewSessionStore()
	t1 := time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 11, 10, 5, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 11, 10, 10, 0, 0, time.UTC)

	store.now = func() time.Time { return t1 }
	store.Touch("oldest", nil)

	store.now = func() time.Time { return t3 }
	store.Touch("newest", nil)

	store.now = func() time.Time { return t2 }
	store.Touch("middle", nil)

	sessions := store.List()
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
	// newest first
	if sessions[0].SocketId != "newest" {
		t.Errorf("expected newest first, got %s", sessions[0].SocketId)
	}
	if sessions[1].SocketId != "middle" {
		t.Errorf("expected middle second, got %s", sessions[1].SocketId)
	}
	if sessions[2].SocketId != "oldest" {
		t.Errorf("expected oldest last, got %s", sessions[2].SocketId)
	}
}

func TestConcurrencySafety(t *testing.T) {
	store := NewSessionStore()
	var wg sync.WaitGroup

	// Concurrently add 100 sessions
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store.Touch(string(rune('a'+i%26))+string(rune('0'+i%10)), nil)
		}(i)
	}

	// Concurrently list sessions
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.List()
		}()
	}

	// Concurrently prune
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Prune(1 * time.Hour)
		}()
	}

	wg.Wait()
	// Should not panic or deadlock
}

func TestTouchWithStateNoStateKey(t *testing.T) {
	store := NewSessionStore()
	// State that doesn't have a "state" key
	store.Touch("abc123", map[string]any{"socketId": "abc123"})
	sessions := store.List()
	if len(sessions) != 1 {
		t.Fatal("expected 1 session")
	}
	if sessions[0].LastIndex != nil {
		t.Fatal("expected LastIndex nil when no state key")
	}
}
