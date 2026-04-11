package stream

import (
	"log"
	"sync"
	"time"
)

// IngestTimeout is the duration after which a live stream receives no ingest
// data before the server auto-stops it. This handles the case where the
// streamer closes the browser tab without pressing "End Stream".
const IngestTimeout = 30 * time.Second

// streamManager is the interface that stream/dash.Manager implements. Defined here
// to avoid a circular import; the concrete *dash.Manager is registered via Register.
type streamManager interface {
	Write(data []byte) error
	Stop() error
}

// registry maps stream ID → active DASH Manager and last-seen ingest time.
var registry struct {
	mu       sync.RWMutex
	managers map[string]streamManager
	lastSeen map[string]time.Time
}

func init() {
	registry.managers = make(map[string]streamManager)
	registry.lastSeen = make(map[string]time.Time)
}

// Register stores a DASH manager for the given stream ID so that ingest
// requests can route data to it.
func Register(streamID string, m streamManager) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.managers[streamID] = m
	registry.lastSeen[streamID] = time.Now()
}

// Unregister removes the HLS manager when a stream ends.
func Unregister(streamID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.managers, streamID)
	delete(registry.lastSeen, streamID)
}

// TouchLastSeen records the current time as the last ingest activity for
// streamID. Called on every successful ingest chunk to reset the timeout.
func TouchLastSeen(streamID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, ok := registry.managers[streamID]; ok {
		registry.lastSeen[streamID] = time.Now()
	}
}

// timedOutStreams returns the IDs of streams that have not received ingest
// data for longer than IngestTimeout.
func timedOutStreams() []string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var ids []string
	for id, last := range registry.lastSeen {
		if time.Since(last) > IngestTimeout {
			ids = append(ids, id)
		}
	}
	return ids
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
