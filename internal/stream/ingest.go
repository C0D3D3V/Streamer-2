package stream

import (
	"log"
	"sync"
)

// streamManager is the interface that stream/dash.Manager implements. Defined here
// to avoid a circular import; the concrete *dash.Manager is registered via Register.
type streamManager interface {
	Write(data []byte) error
	Stop() error
}

// registry maps stream ID → active DASH Manager.
var registry struct {
	mu       sync.RWMutex
	managers map[string]streamManager
}

func init() {
	registry.managers = make(map[string]streamManager)
}

// Register stores a DASH manager for the given stream ID so that ingest
// requests can route data to it.
func Register(streamID string, m streamManager) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.managers[streamID] = m
}

// Unregister removes the HLS manager when a stream ends.
func Unregister(streamID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.managers, streamID)
}

// GetManager returns the active DASH manager for a stream, or nil.
func GetManager(streamID string) streamManager {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.managers[streamID]
}

// StopManager stops and removes the manager for a stream. Safe to call even
// if the manager has already been removed.
func StopManager(streamID string) {
	m := GetManager(streamID)
	if m == nil {
		return
	}
	Unregister(streamID)
	if err := m.Stop(); err != nil {
		log.Printf("ingest[%s]: error stopping HLS manager: %v", streamID, err)
	}
}
