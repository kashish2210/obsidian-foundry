// Package store holds the whole service state behind one lock so every
// invariant (balances, request status, idempotency) changes atomically.
package store

import (
	"encoding/json"
	"sync"
	"time"

	"pocketful/internal/apierr"
)

// Store is the single serialised state core.
type Store struct {
	mu sync.RWMutex
	st *state
}

// Tx is the handle to the state while the store lock is held. Write
// methods must only be used inside Update.
type Tx struct {
	st  *state
	now time.Time
}

// New returns a store with an empty EUR state.
func New() *Store {
	st, err := newState(Data{Currency: "EUR", MinorUnits: 2})
	if err != nil {
		panic(err)
	}
	return &Store{st: st}
}

// Update runs fn with exclusive access to the state.
func (s *Store) Update(fn func(tx *Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&Tx{st: s.st, now: time.Now().UTC().Truncate(time.Microsecond)})
}

// View runs fn with shared read-only access to the state.
func (s *Store) View(fn func(tx *Tx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&Tx{st: s.st, now: time.Now().UTC().Truncate(time.Microsecond)})
}

func (s *Store) replace(st *state) {
	s.mu.Lock()
	s.st = st
	s.mu.Unlock()
}

// Export returns an atomic JSON snapshot of the state.
func (s *Store) Export() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return json.Marshal(&s.st.Data)
}

// Import atomically replaces the state with a snapshot made by Export.
// The store is unchanged when the snapshot is invalid.
func (s *Store) Import(raw []byte) error {
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return apierr.Invalid("state is not a valid snapshot: %v", err)
	}
	st, err := newState(d)
	if err != nil {
		return err
	}
	s.replace(st)
	return nil
}
