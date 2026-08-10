package server

import "sync"

type ConnectionStore struct {
	mu    sync.RWMutex
	conns map[string]DeviceConnection
}

func NewConnectionStore() *ConnectionStore {
	return &ConnectionStore{
		conns: make(map[string]DeviceConnection),
	}
}

func (s *ConnectionStore) Register(deviceID string, conn DeviceConnection) (prev DeviceConnection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev = s.conns[deviceID]
	s.conns[deviceID] = conn
	return prev
}

func (s *ConnectionStore) Get(deviceID string) (DeviceConnection, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.conns[deviceID]
	return c, ok
}

func (s *ConnectionStore) Remove(deviceID string, conn DeviceConnection) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.conns[deviceID]; ok && existing == conn {
		delete(s.conns, deviceID)
		return true
	}
	return false
}

func (s *ConnectionStore) All() []DeviceConnection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conns := make([]DeviceConnection, 0, len(s.conns))
	for _, c := range s.conns {
		conns = append(conns, c)
	}
	return conns
}
