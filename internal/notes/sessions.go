package notes

import (
	"sort"
	"sync"
	"time"
)

// Index represents slide index position (h = horizontal, v = vertical, f = fragment).
type Index struct {
	H *int `json:"h"`
	V *int `json:"v"`
	F *int `json:"f"`
}

// Session represents a connected notes session.
type Session struct {
	SocketId   string    `json:"socketId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	LastIndex  *Index    `json:"lastIndex,omitempty"`
}

// SessionStore is a thread-safe map of sessions keyed by socket ID.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	now      func() time.Time
}

// NewSessionStore creates a new SessionStore.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		now:      time.Now,
	}
}

// Touch creates or updates a session with the given socket ID and optional state data.
// The state map is expected to contain indexh, indexv, indexf as float64 values.
func (s *SessionStore) Touch(socketId string, state map[string]any) {
	if socketId == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	session, exists := s.sessions[socketId]
	if !exists {
		session = &Session{
			SocketId:   socketId,
			CreatedAt:  now,
			LastSeenAt: now,
		}
		s.sessions[socketId] = session
	} else {
		session.LastSeenAt = now
	}

	if state != nil {
		if stateVal, ok := state["state"]; ok {
			if stateMap, ok := stateVal.(map[string]any); ok {
				delete(stateMap, "overview")

				idx := &Index{}
				if h, ok := stateMap["indexh"].(float64); ok {
					v := int(h)
					idx.H = &v
				}
				if v, ok := stateMap["indexv"].(float64); ok {
					v2 := int(v)
					idx.V = &v2
				}
				if f, ok := stateMap["indexf"].(float64); ok {
					v3 := int(f)
					idx.F = &v3
				}
				session.LastIndex = idx
			}
		}
	}
}

// Prune removes sessions that have not been seen since the given TTL duration.
func (s *SessionStore) Prune(ttl time.Duration) {
	cutoff := s.now().Add(-ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	for id, session := range s.sessions {
		if session.LastSeenAt.Before(cutoff) {
			delete(s.sessions, id)
		}
	}
}

// List returns a copy of all sessions sorted by LastSeenAt descending.
func (s *SessionStore) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		cp := *session
		result = append(result, &cp)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].LastSeenAt.After(result[j].LastSeenAt)
	})

	return result
}
