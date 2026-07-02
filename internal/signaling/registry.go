// internal/signaling/registry.go
package signaling

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"rdpAiAnswer/internal/proto"
)

type hostEntry struct {
	name string
}

type Registry struct {
	mu    sync.RWMutex
	hosts map[string]hostEntry
}

func NewRegistry() *Registry {
	return &Registry{hosts: make(map[string]hostEntry)}
}

func (r *Registry) Register(name string) string {
	id := newSessionID()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[id] = hostEntry{name: name}
	return id
}

func (r *Registry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hosts, sessionID)
}

func (r *Registry) List() []proto.SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]proto.SessionInfo, 0, len(r.hosts))
	for id, h := range r.hosts {
		out = append(out, proto.SessionInfo{SessionID: id, Name: h.name, Online: true})
	}
	return out
}

func newSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
