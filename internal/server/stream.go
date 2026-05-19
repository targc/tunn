package server

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Stream struct {
	ID      uint32
	Conn    net.Conn
	Target  string
	Cluster string
	Created time.Time
	Ready   chan struct{}
	closed  atomic.Bool
}

func (s *Stream) Close() {
	if s.closed.CompareAndSwap(false, true) {
		s.Conn.Close()
	}
}

func (s *Stream) IsClosed() bool {
	return s.closed.Load()
}

type StreamManager struct {
	streams map[uint32]*Stream
	nextID  atomic.Uint32
	mu      sync.RWMutex
}

func NewStreamManager() *StreamManager {
	sm := &StreamManager{
		streams: make(map[uint32]*Stream),
	}
	sm.nextID.Store(1)
	return sm
}

func (sm *StreamManager) Create(conn net.Conn, target, cluster string) *Stream {
	id := sm.nextID.Add(1) - 1
	s := &Stream{
		ID:      id,
		Conn:    conn,
		Target:  target,
		Cluster: cluster,
		Created: time.Now(),
		Ready:   make(chan struct{}),
	}

	sm.mu.Lock()
	sm.streams[id] = s
	sm.mu.Unlock()

	return s
}

func (sm *StreamManager) Get(id uint32) (*Stream, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.streams[id]
	return s, ok
}

func (sm *StreamManager) Remove(id uint32) {
	sm.mu.Lock()
	if s, ok := sm.streams[id]; ok {
		s.Close()
		delete(sm.streams, id)
	}
	sm.mu.Unlock()
}

func (sm *StreamManager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for id, s := range sm.streams {
		s.Close()
		delete(sm.streams, id)
	}
}

func (sm *StreamManager) CloseByCluster(cluster string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for id, s := range sm.streams {
		if s.Cluster == cluster {
			s.Close()
			delete(sm.streams, id)
		}
	}
}
