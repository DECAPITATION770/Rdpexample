// internal/signaling/registry_test.go
package signaling

import (
	"sync"
	"testing"
)

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry()
	id := r.Register("PC-OFFICE-1")

	sessions := r.List()
	if len(sessions) != 1 {
		t.Fatalf("List() len = %d, want 1", len(sessions))
	}
	if sessions[0].SessionID != id || sessions[0].Name != "PC-OFFICE-1" || !sessions[0].Online {
		t.Fatalf("unexpected session: %+v", sessions[0])
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	id := r.Register("PC-HOME")
	r.Unregister(id)

	if len(r.List()) != 0 {
		t.Fatalf("List() after Unregister = %d, want 0", len(r.List()))
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := r.Register("concurrent")
			r.List()
			r.Unregister(id)
		}()
	}
	wg.Wait()
	if len(r.List()) != 0 {
		t.Fatalf("List() after concurrent churn = %d, want 0", len(r.List()))
	}
}
