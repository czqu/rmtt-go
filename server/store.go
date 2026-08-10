package server

import "sync"

// ConnectionStore tracks the active device connections by device ID. It is
// safe for concurrent use.
type ConnectionStore struct {
	mu    sync.RWMutex
	conns map[string]DeviceConnection
}

// NewConnectionStore returns an empty connection store.
func NewConnectionStore() *ConnectionStore {
	return &ConnectionStore{
		conns: make(map[string]DeviceConnection),
	}
}

// Register maps deviceID to conn, returning the previous connection for that
// ID (nil when none).
func (s *ConnectionStore) Register(deviceID string, conn DeviceConnection) (prev DeviceConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev = s.conns[deviceID]
	s.conns[deviceID] = conn
	return prev
}

// Get returns the connection registered for deviceID.
func (s *ConnectionStore) Get(deviceID string) (DeviceConnection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conns[deviceID]
	return c, ok
}

// Remove deletes the mapping for deviceID if and only if the currently
// registered connection is conn.
func (s *ConnectionStore) Remove(deviceID string, conn DeviceConnection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.conns[deviceID]; ok && existing == conn {
		delete(s.conns, deviceID)
		return true
	}
	return false
}

// All returns every registered connection.
func (s *ConnectionStore) All() []DeviceConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conns := make([]DeviceConnection, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	return conns
}
