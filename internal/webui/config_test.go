package webui

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestConfigHandler_EmptyServers(t *testing.T) {
	h := ConfigHandler(nil, "", "")
	req := httptest.NewRequest("GET", "/config", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var resp iceConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.ICEServers) != 0 {
		t.Fatalf("ICEServers = %+v, want empty", resp.ICEServers)
	}
}

func TestConfigHandler_WithServers(t *testing.T) {
	h := ConfigHandler([]string{"stun:vps:3478", "turn:vps:3478"}, "rdp", "secret")
	req := httptest.NewRequest("GET", "/config", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	var resp iceConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.ICEServers) != 1 {
		t.Fatalf("ICEServers len = %d, want 1", len(resp.ICEServers))
	}
	entry := resp.ICEServers[0]
	if len(entry.URLs) != 2 || entry.URLs[0] != "stun:vps:3478" || entry.URLs[1] != "turn:vps:3478" {
		t.Fatalf("URLs = %v, want [stun:vps:3478 turn:vps:3478]", entry.URLs)
	}
	if entry.Username != "rdp" || entry.Credential != "secret" {
		t.Fatalf("Username/Credential = %q/%q, want rdp/secret", entry.Username, entry.Credential)
	}
}

func TestConfigHandler_ContentType(t *testing.T) {
	h := ConfigHandler(nil, "", "")
	req := httptest.NewRequest("GET", "/config", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}
