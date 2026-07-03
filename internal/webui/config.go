package webui

import (
	"encoding/json"
	"net/http"
)

type iceServerJSON struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type iceConfigResponse struct {
	ICEServers []iceServerJSON `json:"iceServers"`
}

// ConfigHandler serves the ICE server list (STUN/TURN URLs plus TURN
// credentials, if any) the admin page fetches once on load before
// creating any RTCPeerConnection. urls empty means "no config" — the
// page falls back to an empty ICE server list, fine on a LAN/loopback
// but unable to traverse NAT on a real deployment.
func ConfigHandler(urls []string, turnUsername, turnCredential string) http.HandlerFunc {
	resp := iceConfigResponse{}
	if len(urls) > 0 {
		resp.ICEServers = []iceServerJSON{{URLs: urls, Username: turnUsername, Credential: turnCredential}}
	}
	body, _ := json.Marshal(resp)

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}
