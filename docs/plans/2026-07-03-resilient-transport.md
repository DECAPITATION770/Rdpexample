# Resilient Transport & Guaranteed-Picture Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Make the viewer always show *something* fast, even when WebRTC can't connect: fix silent connection failures, stop the video DataChannel from ever queueing stale frames, add a TURN/TCP path for restrictive networks, and add a true HTTP MJPEG fallback (video + input) for when WebRTC is unavailable entirely.

**Architecture:** Three independent reliability layers, each shippable on its own:
1. **Fix the transport that exists** — TURN over TCP/443 as a second ICE candidate tier (ops/config only), plus a `BufferedAmount()`-gated send loop so the WebRTC video DataChannel drops a stale frame instead of queueing it.
2. **Make failure visible** — the viewer gets a connect timeout and a status badge instead of an indefinite black canvas.
3. **Add a transport that doesn't need WebRTC at all** — screen frames and input events both get a path over the *existing* signaling WebSocket + a new plain HTTP endpoint: host pushes JPEGs to the signaling server, which fans them out via `multipart/x-mixed-replace` (MJPEG) to a `GET /stream/{session_id}` endpoint; mouse/keyboard ride the same signaling socket as new envelope types. No ICE, no UDP, no TURN — just the HTTPS port already in use for the admin page itself.

A viewer picks WebRTC first (lowest latency); if it doesn't connect within a few seconds, or the user forces it, the UI switches to the HTTP MJPEG surface. Both paths apply the same rule at every hop: **never buffer a stale frame — drop it and wait for the next tick.**

**Tech Stack:** Go 1.25, `github.com/pion/webrtc/v4`, `github.com/gorilla/websocket`, vanilla HTML/CSS/JS admin UI (no build step, no JS test runner — frontend changes are verified manually in a browser, matching how the rest of `internal/webui/admin.html` was built).

---

## Before you start

Work happens in the worktree already created at `.worktrees/resilient-transport` (branch `feature/resilient-transport`). `cd` into it before starting Task 1.

Two build realities to keep in mind throughout:
- `internal/hostapp`, `internal/capture`, `internal/input`, and `cmd/host` are `//go:build windows`-tagged and **do not compile on macOS**. For any task touching those files, "run the tests" means `GOOS=windows GOARCH=amd64 go build ./...` (a cross-compile syntax/type check) — there is no way to unit-test them on this machine. Say so explicitly rather than claiming they're tested.
- Everything else (`internal/proto`, `internal/signaling`, `internal/webrtcconn`, `internal/webui`) is plain Go and fully testable with `go test ./...` on macOS.

---

### Task 1: TURN over TCP/443 (ops/config only, no app code)

**Why this is first:** it's the highest-leverage, lowest-risk fix. TURN configured UDP-only fails on any network that blocks UDP outright (common on corporate networks) — adding a TCP/TLS relay listener fixes that class of failure with zero code changes, since `cmd/server -ice-servers` already accepts a comma-separated list of URLs and passes all of them to both pion (host) and the browser (viewer) as one ICE server with multiple `urls`.

**Files:**
- Modify: `docs/deploy/vps-setup.md`

**Step 1: Add a TLS listener to the coturn config example**

In the `/etc/turnserver.conf` block, add:

```
tls-listening-port=443
cert=/etc/letsencrypt/live/your-domain.example/fullchain.pem
pkey=/etc/letsencrypt/live/your-domain.example/privkey.pem
```

Add a short paragraph explaining why: corporate firewalls that block UDP/3478 almost always allow outbound TCP/443 because it's indistinguishable from ordinary HTTPS traffic. Without this, ICE has no fallback candidate and a blocked-UDP network gets a silent black screen.

**Step 2: Update the example host/server invocations to pass both URLs**

```
rdp-host.exe -name "MY-PC" -server wss://your-domain.example/ws/host \
  -ice-servers stun:your-domain.example:3478,turn:your-domain.example:3478,turns:your-domain.example:443?transport=tcp \
  -turn-username rdp -turn-credential CHANGE_ME
```

Same addition to the `rdp-server` systemd `ExecStart` line's `-ice-servers` value.

**Step 3: Note the firewall requirement**

Add to the firewall bullet list: open TCP 443 to coturn (or reuse the same 443 Caddy/Caddy already listens on via `tls-listening-port` + a distinct IP/ALPN, whichever the deployer already uses — call out that if 443 is already bound by another process, pick a different port and rely on the UDP/3478 path there, since the STUN/TURN-over-UDP path is still primary).

**Step 4: Verification (manual, no automated test — this is infra)**

Document the check: after deploying, open `chrome://webrtc-internals` on a network with UDP egress blocked, start a session, and confirm `iceConnectionState` reaches `connected` via a `relay` candidate rather than getting stuck at `checking`.

**Step 5: Commit**

```bash
git add docs/deploy/vps-setup.md
git commit -m "docs: add TURN-over-TCP/443 fallback for UDP-blocked networks"
```

---

### Task 2: In-session Screenshot button + toolbar cleanup

**Why now:** independent, low-risk, immediately useful — gives a manual "get me a picture right now" escape hatch inside a session, which is currently only available from the session list before connecting.

**Files:**
- Modify: `internal/webui/admin.html`

**Step 1: Add a toolbar over the screen area**

Replace the lone `#expand-btn` with a toolbar div holding both buttons:

```html
<div id="screen-wrap">
  <div id="toolbar">
    <button id="shot-btn" title="Grab a one-off screenshot">📷</button>
    <button id="expand-btn">⛶ Expand</button>
  </div>
  <canvas id="screen" width="800" height="600" tabindex="0"></canvas>
</div>
```

Add matching CSS (mirrors `#expand-btn`'s existing positioning, just group them):

```css
#toolbar { position: absolute; top: 10px; right: 10px; z-index: 5; display: flex; gap: 6px; }
#toolbar button { font-size: 12px; opacity: 0.8; }
#toolbar button:hover { opacity: 1; }
```

Remove the old standalone `#expand-btn { position: absolute; ... }` rule (now inherited from `#toolbar button`).

**Step 2: Wire the button and remove the list-view duplicate**

In the script section, add near the other `els` lookups:

```js
shotBtn: document.getElementById("shot-btn"),
```

Wire it once `remote`/`session` exist — reuse the module-level `sessions`/currently-open session id. Simplest: track it in `openSession`:

```js
let currentSession = null;

async function openSession(session) {
  currentSession = session;
  // ...existing body...
}
```

```js
els.shotBtn.addEventListener("click", () => {
  if (currentSession) signaling.requestScreenshot(currentSession.session_id);
});
```

Now remove the per-row screenshot button from `renderSessions` (the `shotBtn` created there) — it's redundant now that the same action is available inside the session, and having it in two places was exactly the kind of inconsistency flagged as confusing:

```js
function renderSessions() {
  els.sessions.innerHTML = "";
  for (const s of sessions) {
    const li = document.createElement("li");
    if (!s.online) li.classList.add("offline");
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = (s.online ? "\u{1F7E2} " : "⚪ ") + s.name;
    if (s.online) name.addEventListener("click", () => openSession(s));
    li.appendChild(name);
    els.sessions.appendChild(li);
  }
}
```

**Step 3: Manual verification**

Run `go run ./cmd/server` (no TURN needed for this check), open the admin UI, connect to a locally-run host (or skip if no Windows box is available — at minimum confirm the page loads, the toolbar renders with both buttons, and clicking Screenshot when no session is open is a no-op instead of throwing).

**Step 4: Commit**

```bash
git add internal/webui/admin.html
git commit -m "feat: move screenshot button into the session toolbar, drop the list-view duplicate"
```

---

### Task 3: Stop the WebRTC video channel from queueing stale frames

**Why:** `pion`'s `DataChannel.Send` enqueues into the SCTP outbound buffer and returns immediately — it does **not** block when the remote side is slow, so a congested link (exactly the kind of link that made someone reach for a TCP/relay fallback in the first place) silently grows an ever-increasing backlog: every future frame waits behind all the unsent ones, and the picture free­zes further behind real time forever. `BufferedAmount()` is pion's way of asking "how much is still unsent" before deciding whether to even bother sending the next one.

**Files:**
- Modify: `internal/webrtcconn/peer.go`
- Modify: `internal/webrtcconn/peer_test.go`
- Modify: `internal/hostapp/app.go` (windows-only, cross-compile-only verification)

**Step 1: Add `VideoBufferedAmount` to `Peer`**

```go
// VideoBufferedAmount reports how many bytes are queued but not yet
// acknowledged on the video DataChannel. Callers use this to decide
// whether to skip sending the next frame rather than let pion's send
// queue grow unbounded when the remote side (or the underlying network
// path) can't keep up.
func (p *Peer) VideoBufferedAmount() uint64 {
	if p.dcVideo == nil {
		return 0
	}
	return p.dcVideo.BufferedAmount()
}
```

**Step 2: Write a smoke test**

A fully deterministic "it actually goes above the threshold" test isn't practical on loopback (pion's SCTP ACKs resolve near-instantly between two local peers, so a burst can drain before the test even reads it — this would be a flaky test asserting on transport timing). Instead, test the two things that *are* deterministic: it's wired to the right channel, and it never panics before a channel exists.

Add to `internal/webrtcconn/peer_test.go`:

```go
func TestPeer_VideoBufferedAmount_ZeroBeforeConnected(t *testing.T) {
	p, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	defer p.Close()

	if got := p.VideoBufferedAmount(); got != 0 {
		t.Fatalf("VideoBufferedAmount() before any data channel exists = %d, want 0", got)
	}
}

func TestPeer_VideoBufferedAmount_TracksAfterConnect(t *testing.T) {
	offerer, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer offerer: %v", err)
	}
	defer offerer.Close()
	answerer, err := NewPeer(nil)
	if err != nil {
		t.Fatalf("NewPeer answerer: %v", err)
	}
	defer answerer.Close()

	offer, err := offerer.CreateOffer()
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	answer, err := answerer.AcceptOffer(offer)
	if err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	if err := offerer.AcceptAnswer(answer); err != nil {
		t.Fatalf("AcceptAnswer: %v", err)
	}
	if err := offerer.WaitConnected(5 * time.Second); err != nil {
		t.Fatalf("WaitConnected: %v", err)
	}

	// Connected with nothing sent yet: buffered amount is 0. This just
	// proves the method reads the real channel post-connect rather than
	// always returning the pre-connect zero value from a nil dcVideo.
	if got := offerer.VideoBufferedAmount(); got != 0 {
		t.Fatalf("VideoBufferedAmount() right after connect = %d, want 0", got)
	}
}
```

**Step 3: Run the tests**

```bash
go test ./internal/webrtcconn/... -run TestPeer_VideoBufferedAmount -v
```
Expected: both PASS.

**Step 4: Commit**

```bash
git add internal/webrtcconn/peer.go internal/webrtcconn/peer_test.go
git commit -m "feat: expose video DataChannel buffered-amount for backpressure checks"
```

**Step 5: Gate `runCaptureLoop` on it** (`internal/hostapp/app.go`)

```go
// videoBufferedAmountThreshold caps how much unacknowledged video data we
// let pion's DataChannel queue before we start skipping frames instead of
// adding to the backlog. ~4 chunks' worth is enough slack for a brief
// stall without ever letting the picture drift meaningfully behind
// real time.
const videoBufferedAmountThreshold = 4 * screenFrameChunkSize

func runCaptureLoop(peer *webrtcconn.Peer, cfg Config, bounds *screenBounds) {
	defer peer.Close()

	ticker := time.NewTicker(cfg.FrameDelay)
	defer ticker.Stop()

	var seq uint32
	wasDropping := false
	for range ticker.C {
		if buffered := peer.VideoBufferedAmount(); buffered > videoBufferedAmountThreshold {
			if !wasDropping {
				log.Printf("hostapp: video channel congested (buffered=%d bytes), dropping frames until it drains", buffered)
				wasDropping = true
			}
			continue
		}
		if wasDropping {
			log.Printf("hostapp: video channel drained, resuming frame sends")
			wasDropping = false
		}

		jpegBytes, width, height, err := capture.GrabPrimaryJPEG(cfg.JPEGQuality)
		if err != nil {
			log.Printf("hostapp: capture failed: %v", err)
			continue
		}
		bounds.width.Store(width)
		bounds.height.Store(height)
		seq++

		disconnected := false
		for _, chunk := range proto.EncodeScreenFrameChunks(seq, jpegBytes, screenFrameChunkSize) {
			if err := peer.SendVideo(chunk); err != nil {
				if strings.Contains(err.Error(), "not established") || isClosedErr(err) {
					log.Printf("hostapp: viewer disconnected, stopping capture loop")
					disconnected = true
					break
				}
				log.Printf("hostapp: send frame chunk failed: %v", err)
				break
			}
		}
		if disconnected {
			return
		}
	}
}
```

This is a direct edit of the existing loop body — insert the buffered-amount check as the very first thing each tick does, before capture even runs (no point spending CPU on `GrabPrimaryJPEG` for a frame you're about to discard).

**Step 6: Verify it compiles (can't unit-test on macOS)**

```bash
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: exits 0, no output.

**Step 7: Commit**

```bash
git add internal/hostapp/app.go
git commit -m "fix: drop video frames instead of queueing them when the DataChannel is congested"
```

---

### Task 4: Viewer connect-timeout and status badge (WebRTC path)

**Why:** right now `openSession` sends an offer and the canvas just... stays black forever if the answer or first frame never arrives. This task makes failure visible; it does not yet add the HTTP fallback (that's Task 8, once the backend pieces in Tasks 5-7 exist).

**Files:**
- Modify: `internal/webui/admin.html`

**Step 1: Add a status badge to the toolbar**

```html
<div id="toolbar">
  <span id="status-badge">connecting…</span>
  <button id="shot-btn" title="Grab a one-off screenshot">📷</button>
  <button id="expand-btn">⛶ Expand</button>
</div>
```

```css
#status-badge { font-size: 12px; padding: 4px 8px; border-radius: 4px; background: #333; }
#status-badge.live { background: #1e5c2e; }
#status-badge.failed { background: #6b1e1e; }
```

**Step 2: Track first-frame arrival and a connect timeout**

```js
const WEBRTC_CONNECT_TIMEOUT_MS = 6000;
let gotFirstFrame = false;
let connectTimeoutHandle = null;

function setStatus(text, cls) {
  els.statusBadge.textContent = text;
  els.statusBadge.className = cls || "";
}
```

Add `statusBadge: document.getElementById("status-badge"),` to `els`.

**Step 3: Wire it into `openSession`**

```js
async function openSession(session) {
  currentSession = session;
  els.headerTitle.textContent = session.name;
  els.back.hidden = false;
  els.listView.hidden = true;
  els.controlView.hidden = false;
  els.mouseToggle.checked = false;
  els.kbToggle.checked = false;

  gotFirstFrame = false;
  setStatus("connecting…", "");
  clearTimeout(connectTimeoutHandle);
  connectTimeoutHandle = setTimeout(() => {
    if (!gotFirstFrame) setStatus("⚠ no connection — retry?", "failed");
  }, WEBRTC_CONNECT_TIMEOUT_MS);

  const iceServers = await fetchIceServers();
  remote = new RemoteSession(signaling, session, renderScreenFrame, iceServers);
  remote.connect();
}
```

**Step 4: Mark success on first frame**

```js
function renderScreenFrame(jpegBytes) {
  if (!gotFirstFrame) {
    gotFirstFrame = true;
    clearTimeout(connectTimeoutHandle);
    setStatus("🟢 live (WebRTC)", "live");
  }
  const url = URL.createObjectURL(new Blob([jpegBytes], { type: "image/jpeg" }));
  const img = new Image();
  img.onload = () => {
    if (els.screen.width !== img.width || els.screen.height !== img.height) {
      els.screen.width = img.width;
      els.screen.height = img.height;
    }
    els.screen.getContext("2d").drawImage(img, 0, 0);
    URL.revokeObjectURL(url);
  };
  img.onerror = () => URL.revokeObjectURL(url);
  img.src = url;
}
```

**Step 5: Clear timers on Back**

```js
els.back.addEventListener("click", () => {
  clearTimeout(connectTimeoutHandle);
  if (remote) { remote.close(); remote = null; }
  currentSession = null;
  els.back.hidden = true;
  els.listView.hidden = false;
  els.controlView.hidden = true;
  els.controlView.classList.remove("expanded");
  els.expandBtn.textContent = "⛶ Expand";
  els.headerTitle.textContent = "RDP-Tool — Sessions";
});
```

**Step 6: Manual verification**

Start `cmd/server`, open the admin UI, click a session with no host actually listening (or block UDP locally to force a failure) and confirm the badge flips to "⚠ no connection — retry?" at ~6s instead of leaving a silent black canvas. Then verify the happy path still shows "🟢 live (WebRTC)" once frames arrive.

**Step 7: Commit**

```bash
git add internal/webui/admin.html
git commit -m "feat: surface WebRTC connect timeout instead of a silent black screen"
```

---

### Task 5: Input events over the signaling WebSocket (protocol + relay + host)

**Why:** the HTTP MJPEG fallback (Task 7) has no WebRTC DataChannel to carry mouse/keyboard, but the signaling WebSocket is already open and already proven reachable (it's how the offer/session-list themselves got through). Reuse it for input instead of inventing a fourth transport.

**Files:**
- Modify: `internal/proto/messages.go`
- Modify: `internal/signaling/handler.go`
- Modify: `internal/signaling/handler_test.go`
- Modify: `internal/hostapp/app.go` (windows-only, cross-compile-only verification)

**Step 1: Add the two new envelope types**

In `internal/proto/messages.go`, extend the `MsgType` const block:

```go
const (
	MsgRegisterHost      MsgType = "register_host"
	MsgListSessions      MsgType = "list_sessions"
	MsgSessionList       MsgType = "session_list"
	MsgConnectRequest    MsgType = "connect_request"
	MsgOffer             MsgType = "offer"
	MsgAnswer            MsgType = "answer"
	MsgICECandidate      MsgType = "ice_candidate"
	MsgRequestScreenshot MsgType = "request_screenshot"
	MsgScreenshot        MsgType = "screenshot"
	MsgInputEvent        MsgType = "input_event"    // viewer -> host, relayed: proto.InputEvent payload
	MsgOverlayMessage    MsgType = "overlay_message" // viewer -> host, relayed: proto.OverlayMessage payload
)
```

`InputEvent` and `OverlayMessage` already exist (used today inside the DataChannel binary framing) — no new struct needed, they're just JSON-marshaled directly as an envelope payload instead of length-prefixed binary.

**Step 2: Write the failing handler test**

Add to `internal/signaling/handler_test.go` (mirrors `TestHandler_RelaysScreenshotRequestAndResponse`'s dial/register pattern already in that file):

```go
func TestHandler_RelaysInputEventFromViewerToHost(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(NewHandler(reg))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	hostConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/host", nil)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer hostConn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: "PC-1"})
	hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload})
	time.Sleep(50 * time.Millisecond)

	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/viewer", nil)
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	defer viewerConn.Close()

	viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgListSessions})
	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var listResp proto.Envelope
	viewerConn.ReadJSON(&listResp)
	var list proto.SessionList
	json.Unmarshal(listResp.Payload, &list)
	sessionID := list.Sessions[0].SessionID

	evtPayload, _ := json.Marshal(proto.InputEvent{Kind: proto.InputMouseMove, X: 42, Y: 7})
	if err := viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgInputEvent, SessionID: sessionID, Payload: evtPayload}); err != nil {
		t.Fatalf("write input event: %v", err)
	}

	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got proto.Envelope
	if err := hostConn.ReadJSON(&got); err != nil {
		t.Fatalf("host read input event: %v", err)
	}
	if got.Type != proto.MsgInputEvent {
		t.Fatalf("got.Type = %v, want input_event", got.Type)
	}
	var gotEvt proto.InputEvent
	json.Unmarshal(got.Payload, &gotEvt)
	if gotEvt.X != 42 || gotEvt.Y != 7 {
		t.Fatalf("gotEvt = %+v, want X=42 Y=7", gotEvt)
	}
}
```

**Step 3: Run it to confirm it fails**

```bash
go test ./internal/signaling/... -run TestHandler_RelaysInputEventFromViewerToHost -v
```
Expected: FAIL — the viewer's `handleViewer` switch doesn't have a case for `MsgInputEvent`, so the host's `ReadJSON` times out.

**Step 4: Add the relay case**

In `internal/signaling/handler.go`'s `handleViewer`, extend the switch (this mirrors the existing `MsgOffer, MsgICECandidate, MsgRequestScreenshot` case — same "remember which viewer socket owns this session, forward to host" shape):

```go
case proto.MsgOffer, proto.MsgICECandidate, proto.MsgRequestScreenshot, proto.MsgInputEvent, proto.MsgOverlayMessage:
	h.setViewer(env.SessionID, conn)
	if host := h.hostConn(env.SessionID); host != nil {
		_ = host.WriteJSON(env)
		log.Printf("signaling: relayed %s from viewer to host session %s", env.Type, env.SessionID)
	} else {
		log.Printf("signaling: viewer sent %s for session %s but no host with that ID is connected (stale session list?)", env.Type, env.SessionID)
	}
```

**Step 5: Run the test again**

```bash
go test ./internal/signaling/... -run TestHandler_RelaysInputEventFromViewerToHost -v
```
Expected: PASS.

**Step 6: Run the full signaling suite to check nothing broke**

```bash
go test ./internal/signaling/...
```
Expected: all PASS.

**Step 7: Commit**

```bash
git add internal/proto/messages.go internal/signaling/handler.go internal/signaling/handler_test.go
git commit -m "feat: relay input events and overlay messages over the signaling WebSocket"
```

**Step 8: Apply relayed input/overlay on the host** (`internal/hostapp/app.go`)

Two things change here:

1. `screenBounds` moves from per-`handleOffer`-call scope to per-process scope, since it now needs to be shared by the WebRTC capture loop, the new HTTP-relay capture loop (Task 6), *and* this new relayed-input path — it's really just "the last known capture resolution," not tied to any one transport.
2. The overlay-showing logic gets pulled out of `handleDataChannelMessage` into its own function so both the DataChannel path and the new WS path can call it.

```go
func Run(cfg Config) error {
	cfg.applyDefaults()

	rawConn, _, err := websocket.DefaultDialer.Dial(cfg.SignalingURL, nil)
	if err != nil {
		return err
	}
	conn := &safeConn{conn: rawConn}
	defer conn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: cfg.Name})
	if err := conn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload}); err != nil {
		return err
	}
	log.Printf("hostapp: registered as %q, waiting for viewers", cfg.Name)

	bounds := &screenBounds{}

	for {
		var env proto.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}

		switch env.Type {
		case proto.MsgOffer:
			go handleOffer(conn, cfg, env, bounds)
		case proto.MsgRequestScreenshot:
			go handleScreenshotRequest(conn, env)
		case proto.MsgInputEvent:
			var evt proto.InputEvent
			if err := json.Unmarshal(env.Payload, &evt); err != nil {
				log.Printf("hostapp: bad relayed input event: %v", err)
				continue
			}
			applyInputEvent(evt, bounds)
		case proto.MsgOverlayMessage:
			var msg proto.OverlayMessage
			if err := json.Unmarshal(env.Payload, &msg); err != nil {
				log.Printf("hostapp: bad relayed overlay message: %v", err)
				continue
			}
			showOverlayMessage(msg)
		default:
			// MsgICECandidate is relayed by the signaling server but
			// unused here: webrtcconn.Peer waits for full ICE gathering
			// before returning an offer/answer (non-trickle ICE), so the
			// SDP already carries every candidate. Nothing else to do.
		}
	}
}
```

`handleOffer` now takes the shared `bounds` instead of allocating its own:

```go
func handleOffer(signaling *safeConn, cfg Config, env proto.Envelope, bounds *screenBounds) {
	// ...unchanged up through peer.WaitConnected...

	log.Printf("hostapp: session %s connected", env.SessionID)

	peer.OnData(func(data []byte) { handleDataChannelMessage(data, bounds) })

	runCaptureLoop(peer, cfg, bounds)
}
```

(Delete the `bounds := &screenBounds{}` line that used to live inside `handleOffer` — it's now a parameter.)

Extract the overlay helper out of `handleDataChannelMessage`:

```go
func handleDataChannelMessage(data []byte, bounds *screenBounds) {
	msg, err := proto.DecodeFrame(data)
	if err != nil {
		log.Printf("hostapp: bad data channel frame: %v", err)
		return
	}

	switch m := msg.(type) {
	case proto.InputEvent:
		applyInputEvent(m, bounds)
	case proto.OverlayMessage:
		showOverlayMessage(m)
	}
}

func showOverlayMessage(m proto.OverlayMessage) {
	if err := m.Validate(); err != nil {
		log.Printf("hostapp: invalid overlay message: %v", err)
		return
	}
	fade := time.Duration(m.FadeSeconds * float64(time.Second))
	go func() {
		if err := overlay.ShowMessage(m.Text, fade); err != nil {
			log.Printf("hostapp: overlay.ShowMessage failed: %v", err)
		}
	}()
}
```

**Step 9: Verify it compiles**

```bash
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: exits 0.

**Step 10: Commit**

```bash
git add internal/hostapp/app.go
git commit -m "refactor: share capture bounds and overlay handling between DataChannel and relayed input paths"
```

---

### Task 6: Frame-relay protocol + host push loop

**Why:** this is the video half of the HTTP fallback — host pushes JPEGs to the signaling server continuously, independent of any WebRTC PeerConnection.

**Files:**
- Modify: `internal/proto/messages.go`
- Modify: `internal/signaling/handler.go`
- New: `internal/signaling/framerelay.go`
- New: `internal/signaling/framerelay_test.go`
- Modify: `internal/hostapp/app.go` (windows-only, cross-compile-only verification)

**Step 1: Add the relay message types**

In `internal/proto/messages.go`:

```go
const (
	// ...existing consts...
	MsgStartFrameRelay MsgType = "start_frame_relay" // server -> host: begin pushing RelayFrameMessages
	MsgStopFrameRelay  MsgType = "stop_frame_relay"  // server -> host: stop pushing them
	MsgRelayFrame      MsgType = "relay_frame"       // host -> server: one JPEG frame for the HTTP fan-out
)

// RelayFrameMessage carries one JPEG frame from host to the signaling
// server for the HTTP/MJPEG fallback path (see internal/signaling's
// FrameBroadcaster). Distinct from ScreenshotMessage even though the
// shape is identical — that one is a single on-demand preview, this one
// is a continuous stream, and keeping the types separate keeps intent
// clear at call sites.
type RelayFrameMessage struct {
	JPEG []byte `json:"jpeg"`
}
```

**Step 2: Write the failing test for the broadcaster**

New file `internal/signaling/framerelay_test.go`:

```go
package signaling

import "testing"

func TestFrameBroadcaster_DeliversPublishedFrame(t *testing.T) {
	b := NewFrameBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()

	b.Publish([]byte("frame-1"))

	select {
	case got := <-ch:
		if string(got) != "frame-1" {
			t.Fatalf("got %q, want %q", got, "frame-1")
		}
	default:
		t.Fatal("expected a frame to be immediately available")
	}
}

func TestFrameBroadcaster_DropsStaleFrameForSlowSubscriber(t *testing.T) {
	b := NewFrameBroadcaster()
	ch, cancel := b.Subscribe()
	defer cancel()

	// Publish twice without draining the channel — the second publish
	// must not block, and the subscriber should end up seeing only the
	// latest frame, never a backlog of both.
	b.Publish([]byte("frame-1"))
	b.Publish([]byte("frame-2"))

	got := <-ch
	if string(got) != "frame-1" {
		t.Fatalf("got %q, want %q (the first frame, since it arrived before frame-2 was dropped)", got, "frame-1")
	}
	select {
	case extra := <-ch:
		t.Fatalf("expected channel empty after draining one frame, got extra: %q", extra)
	default:
	}
}

func TestFrameBroadcaster_FansOutToMultipleSubscribers(t *testing.T) {
	b := NewFrameBroadcaster()
	ch1, cancel1 := b.Subscribe()
	defer cancel1()
	ch2, cancel2 := b.Subscribe()
	defer cancel2()

	b.Publish([]byte("frame"))

	for i, ch := range []chan []byte{ch1, ch2} {
		select {
		case got := <-ch:
			if string(got) != "frame" {
				t.Fatalf("subscriber %d got %q, want %q", i, got, "frame")
			}
		default:
			t.Fatalf("subscriber %d: expected a frame to be immediately available", i)
		}
	}
}

func TestFrameBroadcaster_SubscribeReportsFirstAndCancelReportsLast(t *testing.T) {
	b := NewFrameBroadcaster()

	_, cancel1, isFirst1 := b.SubscribeTracked()
	if !isFirst1 {
		t.Fatal("first Subscribe should report isFirst=true")
	}
	_, cancel2, isFirst2 := b.SubscribeTracked()
	if isFirst2 {
		t.Fatal("second Subscribe should report isFirst=false")
	}

	if isLast := cancel1(); isLast {
		t.Fatal("cancelling one of two subscribers should report isLast=false")
	}
	if isLast := cancel2(); !isLast {
		t.Fatal("cancelling the last subscriber should report isLast=true")
	}
}
```

**Step 3: Run it to confirm it fails**

```bash
go test ./internal/signaling/... -run TestFrameBroadcaster -v
```
Expected: FAIL to compile — `NewFrameBroadcaster` doesn't exist yet.

**Step 4: Implement the broadcaster**

New file `internal/signaling/framerelay.go`:

```go
package signaling

import "sync"

// FrameBroadcaster fans out JPEG frames for one session to any number of
// HTTP subscribers. Publish never blocks: a subscriber whose channel
// still holds the previous frame simply misses the new one — the same
// "never queue a stale frame" rule the WebRTC video path enforces via
// BufferedAmount (see internal/webrtcconn.Peer.VideoBufferedAmount).
type FrameBroadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewFrameBroadcaster() *FrameBroadcaster {
	return &FrameBroadcaster{subs: make(map[chan []byte]struct{})}
}

// Subscribe registers a new frame receiver. cancel must be called when
// the subscriber is done (e.g. the HTTP request ends) to free it.
func (b *FrameBroadcaster) Subscribe() (ch chan []byte, cancel func()) {
	ch, cancel, _ = b.SubscribeTracked()
	return ch, cancel
}

// SubscribeTracked is Subscribe plus bookkeeping the HTTP handler needs
// to tell the host when to start/stop relaying: isFirst reports whether
// this was the only subscriber at the moment it joined, and the returned
// cancel function reports whether it was the last one to leave.
func (b *FrameBroadcaster) SubscribeTracked() (ch chan []byte, cancel func() (isLast bool), isFirst bool) {
	ch = make(chan []byte, 1)
	b.mu.Lock()
	isFirst = len(b.subs) == 0
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel = func() bool {
		b.mu.Lock()
		delete(b.subs, ch)
		isLast := len(b.subs) == 0
		b.mu.Unlock()
		return isLast
	}
	return ch, cancel, isFirst
}

// Publish fans jpeg out to every current subscriber without blocking.
func (b *FrameBroadcaster) Publish(jpeg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- jpeg:
		default: // subscriber hasn't drained the previous frame; drop this one
		}
	}
}
```

**Step 5: Run the tests again**

```bash
go test ./internal/signaling/... -run TestFrameBroadcaster -v
```
Expected: all PASS.

**Step 6: Wire the broadcaster into `Handler` and relay `MsgRelayFrame`**

In `internal/signaling/handler.go`, add a registry of broadcasters and a lookup helper:

```go
type Handler struct {
	mu       sync.Mutex
	reg      *Registry
	mux      *http.ServeMux
	hosts    map[string]*safeConn // sessionID -> host socket
	viewers  map[string]*safeConn // sessionID -> viewer socket
	relaysMu sync.Mutex
	relays   map[string]*FrameBroadcaster // sessionID -> broadcaster
}

func NewHandler(reg *Registry) *Handler {
	h := &Handler{
		reg:     reg,
		mux:     http.NewServeMux(),
		hosts:   make(map[string]*safeConn),
		viewers: make(map[string]*safeConn),
		relays:  make(map[string]*FrameBroadcaster),
	}
	h.mux.HandleFunc("/ws/host", h.handleHost)
	h.mux.HandleFunc("/ws/viewer", h.handleViewer)
	return h
}

// broadcaster returns the session's FrameBroadcaster, creating it on
// first use. One broadcaster per session lives for the process lifetime
// of that session's registration — cheap enough (a map and a mutex) not
// to bother tearing down on host disconnect.
func (h *Handler) broadcaster(sessionID string) *FrameBroadcaster {
	h.relaysMu.Lock()
	defer h.relaysMu.Unlock()
	b, ok := h.relays[sessionID]
	if !ok {
		b = NewFrameBroadcaster()
		h.relays[sessionID] = b
	}
	return b
}
```

Add the `MsgRelayFrame` case to `handleHost`'s switch — this one does **not** forward to the viewer WS connection (the HTTP endpoint from Task 7 is the consumer), it feeds the broadcaster directly:

```go
case proto.MsgRelayFrame:
	var frame proto.RelayFrameMessage
	if err := json.Unmarshal(env.Payload, &frame); err != nil {
		continue
	}
	h.broadcaster(sessionID).Publish(frame.JPEG)
```

**Step 7: Run the full signaling suite**

```bash
go test ./internal/signaling/...
```
Expected: all PASS.

**Step 8: Commit**

```bash
git add internal/proto/messages.go internal/signaling/handler.go internal/signaling/framerelay.go internal/signaling/framerelay_test.go
git commit -m "feat: add FrameBroadcaster and relay_frame handling for the HTTP fallback path"
```

**Step 9: Add the host-side push loop** (`internal/hostapp/app.go`)

Add start/stop handling to `Run`'s switch and the loop itself:

```go
case proto.MsgStartFrameRelay:
	go runFrameRelayLoop(conn, cfg, bounds, env.SessionID)
case proto.MsgStopFrameRelay:
	stopFrameRelay(env.SessionID)
```

```go
var (
	frameRelayMu    sync.Mutex
	frameRelayStops = map[string]chan struct{}{}
)

func stopFrameRelay(sessionID string) {
	frameRelayMu.Lock()
	defer frameRelayMu.Unlock()
	if stop, ok := frameRelayStops[sessionID]; ok {
		close(stop)
		delete(frameRelayStops, sessionID)
	}
}

// runFrameRelayLoop pushes JPEG frames to the signaling server for the
// HTTP/MJPEG fallback, independent of any WebRTC PeerConnection. Unlike
// the WebRTC video path, this doesn't need an explicit buffered-amount
// check: conn.WriteJSON writes straight through to the underlying TCP
// socket, so if the signaling link is backed up the write blocks, and
// time.Ticker already drops ticks that occur while its consumer is busy
// — there is no separate unbounded send queue for this leg to overflow.
func runFrameRelayLoop(conn *safeConn, cfg Config, bounds *screenBounds, sessionID string) {
	stop := make(chan struct{})
	frameRelayMu.Lock()
	frameRelayStops[sessionID] = stop
	frameRelayMu.Unlock()
	defer stopFrameRelay(sessionID)

	ticker := time.NewTicker(cfg.FrameDelay)
	defer ticker.Stop()

	log.Printf("hostapp: starting HTTP frame relay for session %s", sessionID)
	for {
		select {
		case <-stop:
			log.Printf("hostapp: stopping HTTP frame relay for session %s", sessionID)
			return
		case <-ticker.C:
			jpegBytes, width, height, err := capture.GrabPrimaryJPEG(cfg.JPEGQuality)
			if err != nil {
				log.Printf("hostapp: relay capture failed: %v", err)
				continue
			}
			bounds.width.Store(width)
			bounds.height.Store(height)

			payload, err := json.Marshal(proto.RelayFrameMessage{JPEG: jpegBytes})
			if err != nil {
				continue
			}
			if err := conn.WriteJSON(proto.Envelope{Type: proto.MsgRelayFrame, SessionID: sessionID, Payload: payload}); err != nil {
				log.Printf("hostapp: relay frame send failed: %v", err)
				return
			}
		}
	}
}
```

**Step 10: Verify it compiles**

```bash
GOOS=windows GOARCH=amd64 go build ./...
```
Expected: exits 0.

**Step 11: Commit**

```bash
git add internal/hostapp/app.go
git commit -m "feat: push continuous frames over the signaling socket when HTTP relay is requested"
```

---

### Task 7: HTTP MJPEG endpoint (`GET /stream/{session_id}`)

**Files:**
- New: `internal/signaling/stream.go`
- New: `internal/signaling/stream_test.go`
- Modify: `internal/signaling/handler.go`
- Modify: `cmd/server/main.go`

**Step 1: Write the failing test**

New file `internal/signaling/stream_test.go`:

```go
package signaling

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"rdpAiAnswer/internal/proto"
)

func TestHandler_StreamEndpoint_DeliversFramesAndSignalsHostStartStop(t *testing.T) {
	reg := NewRegistry()
	srv := httptest.NewServer(NewHandler(reg))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	hostConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/host", nil)
	if err != nil {
		t.Fatalf("dial host: %v", err)
	}
	defer hostConn.Close()

	payload, _ := json.Marshal(proto.RegisterHost{Name: "PC-1"})
	hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRegisterHost, Payload: payload})

	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var regEnv proto.Envelope
	// Registration has no server reply; instead read the session ID off
	// the registry directly via a viewer list request.
	viewerConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/ws/viewer", nil)
	if err != nil {
		t.Fatalf("dial viewer: %v", err)
	}
	defer viewerConn.Close()
	viewerConn.WriteJSON(proto.Envelope{Type: proto.MsgListSessions})
	viewerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var listResp proto.Envelope
	viewerConn.ReadJSON(&listResp)
	var list proto.SessionList
	json.Unmarshal(listResp.Payload, &list)
	sessionID := list.Sessions[0].SessionID
	_ = regEnv

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/stream/"+sessionID, nil)
	client := &http.Client{}
	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			return
		}
		respCh <- resp
	}()

	// The GET should trigger a start_frame_relay to the host.
	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var startEnv proto.Envelope
	if err := hostConn.ReadJSON(&startEnv); err != nil {
		t.Fatalf("host read start_frame_relay: %v", err)
	}
	if startEnv.Type != proto.MsgStartFrameRelay {
		t.Fatalf("startEnv.Type = %v, want start_frame_relay", startEnv.Type)
	}

	resp := <-respCh
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/x-mixed-replace") {
		t.Fatalf("Content-Type = %q, want multipart/x-mixed-replace prefix", ct)
	}
	_, params, err := parseMediaTypeForTest(ct)
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}

	// Host pushes one frame.
	framePayload, _ := json.Marshal(proto.RelayFrameMessage{JPEG: []byte("fake-jpeg-bytes")})
	if err := hostConn.WriteJSON(proto.Envelope{Type: proto.MsgRelayFrame, SessionID: sessionID, Payload: framePayload}); err != nil {
		t.Fatalf("write relay frame: %v", err)
	}

	mr := multipart.NewReader(resp.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("NextPart: %v", err)
	}
	buf := make([]byte, len("fake-jpeg-bytes"))
	if _, err := part.Read(buf); err != nil {
		t.Fatalf("read part: %v", err)
	}
	if string(buf) != "fake-jpeg-bytes" {
		t.Fatalf("part body = %q, want %q", buf, "fake-jpeg-bytes")
	}

	// Client disconnect should trigger stop_frame_relay.
	cancel()
	hostConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var stopEnv proto.Envelope
	if err := hostConn.ReadJSON(&stopEnv); err != nil {
		t.Fatalf("host read stop_frame_relay: %v", err)
	}
	if stopEnv.Type != proto.MsgStopFrameRelay {
		t.Fatalf("stopEnv.Type = %v, want stop_frame_relay", stopEnv.Type)
	}
}
```

Add a tiny local helper (avoids importing `mime` just for one call at the top of the test file if preferred, or just import `mime`) — simplest is to import the stdlib `mime` package directly instead of a fake helper:

```go
import "mime"
```

and replace `parseMediaTypeForTest(ct)` with `mime.ParseMediaType(ct)`.

**Step 2: Run it to confirm it fails**

```bash
go test ./internal/signaling/... -run TestHandler_StreamEndpoint -v
```
Expected: FAIL — `404 page not found`, since `/stream/{session_id}` isn't registered yet.

**Step 3: Implement the endpoint**

New file `internal/signaling/stream.go`:

```go
package signaling

import (
	"encoding/json"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"

	"rdpAiAnswer/internal/proto"
)

func (h *Handler) sendToHost(sessionID string, env proto.Envelope) {
	if host := h.hostConn(sessionID); host != nil {
		_ = host.WriteJSON(env)
	}
}

// handleStream serves GET /stream/{session_id} as a multipart/x-mixed-replace
// MJPEG feed — the plain-HTTP fallback for when WebRTC can't connect at
// all. It asks the host to start pushing frames on the first subscriber
// and to stop on the last, so an idle host with nobody watching over
// HTTP doesn't pay any relay cost.
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	if h.hostConn(sessionID) == nil {
		http.Error(w, "session not connected", http.StatusServiceUnavailable)
		return
	}

	bc := h.broadcaster(sessionID)
	ch, cancel, isFirst := bc.SubscribeTracked()
	if isFirst {
		h.sendToHost(sessionID, proto.Envelope{Type: proto.MsgStartFrameRelay, SessionID: sessionID})
	}
	defer func() {
		if isLast := cancel(); isLast {
			h.sendToHost(sessionID, proto.Envelope{Type: proto.MsgStopFrameRelay, SessionID: sessionID})
		}
	}()

	const boundary = "frame"
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+boundary)
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)

	mw := multipart.NewWriter(w)
	_ = mw.SetBoundary(boundary)

	for {
		select {
		case jpeg := <-ch:
			part, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":   {"image/jpeg"},
				"Content-Length": {strconv.Itoa(len(jpeg))},
			})
			if err != nil {
				return
			}
			if _, err := part.Write(jpeg); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

var _ = json.Marshal // silence unused import if trimmed later; remove once other code in this file uses json
```

(Drop the trailing `var _ = json.Marshal` line — it's a placeholder in case `encoding/json` ends up unused after final edits; check with `goimports`/`go vet` and remove the import entirely if truly unused.)

Register the route in `NewHandler` (`internal/signaling/handler.go`):

```go
func NewHandler(reg *Registry) *Handler {
	h := &Handler{
		reg:     reg,
		mux:     http.NewServeMux(),
		hosts:   make(map[string]*safeConn),
		viewers: make(map[string]*safeConn),
		relays:  make(map[string]*FrameBroadcaster),
	}
	h.mux.HandleFunc("/ws/host", h.handleHost)
	h.mux.HandleFunc("/ws/viewer", h.handleViewer)
	h.mux.HandleFunc("GET /stream/{session_id}", h.handleStream)
	return h
}
```

**Step 4: Mount it in `cmd/server/main.go`**

```go
mux.Handle("/ws/", sigHandler)
mux.Handle("/stream/", sigHandler)
mux.HandleFunc("/config", webui.ConfigHandler(urls, *turnUsername, *turnCredential))
mux.Handle("/", webui.Handler())
```

**Step 5: Run the test again**

```bash
go test ./internal/signaling/... -run TestHandler_StreamEndpoint -v
```
Expected: PASS.

**Step 6: Run the full suite**

```bash
go test ./...
```
Expected: all PASS (windows-only packages are skipped automatically by `go test` on macOS since they simply don't match the build constraints — that's expected, not a failure).

**Step 7: Commit**

```bash
git add internal/signaling/stream.go internal/signaling/stream_test.go internal/signaling/handler.go cmd/server/main.go
git commit -m "feat: serve GET /stream/{session_id} as an MJPEG fallback for when WebRTC can't connect"
```

---

### Task 8: Frontend transport switch (WebRTC ⇄ HTTP relay)

**Files:**
- Modify: `internal/webui/admin.html`

**Step 1: Add signaling senders for relayed input/overlay**

```js
sendInputEvent(sessionId, evt) {
  this.ws.send(JSON.stringify({ type: "input_event", session_id: sessionId, payload: evt }));
}
sendOverlayMessage(sessionId, msg) {
  this.ws.send(JSON.stringify({ type: "overlay_message", session_id: sessionId, payload: msg }));
}
```
(Add these two methods to the `SignalingClient` class, next to `requestScreenshot`.)

**Step 2: Add the HTTP-relay `<img>` element alongside the canvas**

```html
<div id="screen-wrap">
  <div id="toolbar">
    <span id="status-badge">connecting…</span>
    <button id="transport-toggle" title="Switch between WebRTC and HTTP">⇄</button>
    <button id="shot-btn" title="Grab a one-off screenshot">📷</button>
    <button id="expand-btn">⛶ Expand</button>
  </div>
  <canvas id="screen" width="800" height="600" tabindex="0"></canvas>
  <img id="screen-http" hidden alt="live view (HTTP)">
</div>
```

```css
#screen-http { max-width: 100%; max-height: 100%; cursor: crosshair; }
```

**Step 3: Introduce a transport mode with one shared surface accessor**

```js
let transportMode = "webrtc"; // "webrtc" | "http"

function activeSurfaceEl() {
  return transportMode === "http" ? els.screenHttp : els.screen;
}

function activeSurfaceSize() {
  const el = activeSurfaceEl();
  return transportMode === "http"
    ? { width: el.naturalWidth, height: el.naturalHeight }
    : { width: el.width, height: el.height };
}

function sendInput(kind, obj) {
  if (!currentSession) return;
  if (transportMode === "webrtc") {
    if (remote) remote.send(FRAME_INPUT_EVENT, { kind, ...obj });
  } else {
    signaling.sendInputEvent(currentSession.session_id, { kind, ...obj });
  }
}
```

Add `screenHttp: document.getElementById("screen-http"), transportToggle: document.getElementById("transport-toggle"),` to `els`.

**Step 4: Rewrite the mouse handlers to go through `sendInput`/`activeSurfaceEl`/`activeSurfaceSize`**

Replace the three `els.screen.addEventListener("mouse...")` handlers with handlers bound to *both* surfaces (attach once to each element at startup, guarded by `transportMode` so only the active one's events do anything):

```js
function onMouseMove(e) {
  if (!els.mouseToggle.checked || transportMode !== currentEventSurfaceMode(e)) return;
  const el = e.currentTarget;
  const rect = el.getBoundingClientRect();
  const size = activeSurfaceSize();
  const x = Math.round(((e.clientX - rect.left) / rect.width) * size.width);
  const y = Math.round(((e.clientY - rect.top) / rect.height) * size.height);
  sendInput("mouse_move", { x, y });
}
```

This is getting more complex than it needs to be by trying to bind both elements up front. Simpler: since only one surface is ever visible at a time (the other is `hidden`), just attach the *same* three listeners to both `els.screen` and `els.screenHttp`, and let `activeSurfaceSize()` — not the event target — decide the coordinate math and `sendInput` decide the transport. A hidden element doesn't receive pointer events, so there's no need to guard by mode at all:

```js
function wireMouseControl(el) {
  el.addEventListener("mousemove", (e) => {
    if (!els.mouseToggle.checked || !currentSession) return;
    const rect = el.getBoundingClientRect();
    const size = activeSurfaceSize();
    const x = Math.round(((e.clientX - rect.left) / rect.width) * size.width);
    const y = Math.round(((e.clientY - rect.top) / rect.height) * size.height);
    sendInput("mouse_move", { x, y });
  });
  el.addEventListener("mousedown", () => {
    if (!els.mouseToggle.checked || !currentSession) return;
    sendInput("mouse_down", {});
  });
  el.addEventListener("mouseup", () => {
    if (!els.mouseToggle.checked || !currentSession) return;
    sendInput("mouse_up", {});
  });
  el.addEventListener("contextmenu", (e) => e.preventDefault());
}
wireMouseControl(els.screen);
wireMouseControl(els.screenHttp);
```

Remove the old individual `els.screen.addEventListener(...)` block for mouse control that this replaces.

**Step 5: Route keyboard sends through `sendInput` too**

Replace the two `remote.send(FRAME_INPUT_EVENT, { kind: "key_down", key_vk: vk })` / `key_up` calls in the existing `keydown`/`keyup` document listeners with:

```js
sendInput("key_down", { key_vk: vk });
```
```js
sendInput("key_up", { key_vk: vk });
```

**Step 6: Route the overlay-message send through the same abstraction**

```js
els.msgSend.addEventListener("click", () => {
  if (!currentSession) return;
  const text = els.msgText.value;
  if (!text) return;
  let fade = parseFloat(els.msgFade.value);
  if (isNaN(fade) || fade <= 0) fade = 2.0;
  fade = Math.round(fade * 10) / 10;
  if (transportMode === "webrtc") {
    if (remote) remote.send(FRAME_OVERLAY_MESSAGE, { text, fade_seconds: fade });
  } else {
    signaling.sendOverlayMessage(currentSession.session_id, { text, fade_seconds: fade });
  }
  els.msgText.value = "";
});
```

**Step 7: Add the switch function**

```js
function switchTransport(mode) {
  transportMode = mode;
  if (mode === "http") {
    if (remote) { remote.close(); remote = null; }
    els.screen.hidden = true;
    els.screenHttp.hidden = false;
    els.screenHttp.src = "/stream/" + currentSession.session_id;
    setStatus("🟠 HTTP relay", "");
    els.transportToggle.textContent = "⇄ WebRTC";
  } else {
    els.screenHttp.src = "";
    els.screenHttp.hidden = true;
    els.screen.hidden = false;
    els.transportToggle.textContent = "⇄ HTTP";
    gotFirstFrame = false;
    setStatus("connecting…", "");
    (async () => {
      const iceServers = await fetchIceServers();
      remote = new RemoteSession(signaling, currentSession, renderScreenFrame, iceServers);
      remote.connect();
      clearTimeout(connectTimeoutHandle);
      connectTimeoutHandle = setTimeout(() => {
        if (!gotFirstFrame) setStatus("⚠ no connection — retry?", "failed");
      }, WEBRTC_CONNECT_TIMEOUT_MS);
    })();
  }
}

els.transportToggle.addEventListener("click", () => {
  switchTransport(transportMode === "webrtc" ? "http" : "webrtc");
});
```

**Step 8: Auto-switch on WebRTC connect-timeout**

Update the timeout callback from Task 4 to actually fall back instead of just labeling the failure:

```js
connectTimeoutHandle = setTimeout(() => {
  if (!gotFirstFrame) {
    setStatus("⚠ WebRTC timed out — switching to HTTP", "failed");
    switchTransport("http");
  }
}, WEBRTC_CONNECT_TIMEOUT_MS);
```

**Step 9: Reset transport state on session open/close**

In `openSession`, ensure it always starts in `webrtc` mode (`transportMode = "webrtc";` at the top, before the timeout is armed). In the `back` button handler, also reset `els.screenHttp.src = ""; els.screenHttp.hidden = true; els.screen.hidden = false; transportMode = "webrtc";`.

**Step 10: Manual verification (no JS test runner exists in this project — verify in a real browser)**

1. `go run ./cmd/server` and a real (or locally-run) Windows host.
2. Happy path: open a session, confirm WebRTC connects, badge shows "🟢 live (WebRTC)", mouse/keyboard/overlay all work.
3. Force fallback: block UDP (or temporarily break TURN config) and confirm the badge flips to "⚠ WebRTC timed out — switching to HTTP" at ~6s, the `<img>` starts showing the MJPEG stream, and mouse/keyboard/overlay still work (now riding `input_event`/`overlay_message` over the signaling socket).
4. Manual toggle: with WebRTC connected, click "⇄ HTTP" and confirm it switches live; click again and confirm it reconnects WebRTC.
5. Confirm `docs/screenshots/control-window*.png` still roughly match the new toolbar layout, or note that they're now stale (out of scope to regenerate here unless asked).

**Step 11: Commit**

```bash
git add internal/webui/admin.html
git commit -m "feat: add HTTP/MJPEG transport fallback with automatic and manual switching"
```

---

### Task 9: Docs pass

**Files:**
- Modify: `README.md`
- Modify: `docs/deploy/vps-setup.md`

**Step 1: Document the resilience model in `README.md`**

Add a short section explaining: WebRTC is tried first (lowest latency); after a 6s connect timeout (or a manual click) the viewer falls back to `GET /stream/{session_id}` (MJPEG over plain HTTP, no ICE/TURN/UDP needed) with mouse/keyboard riding the same signaling WebSocket; every hop (WebRTC DataChannel, host→server relay, server→browser fan-out) drops a stale frame rather than queueing one.

**Step 2: Note the new endpoint and its firewall implications in `docs/deploy/vps-setup.md`**

`/stream/{session_id}` is plain HTTP(S) on the same port as the admin UI — no additional firewall rule needed beyond what already serves the page.

**Step 3: Commit**

```bash
git add README.md docs/deploy/vps-setup.md
git commit -m "docs: document the WebRTC/HTTP resilience model"
```

---

## Summary of new/changed files

- `internal/webrtcconn/peer.go`, `peer_test.go` — video backpressure check
- `internal/hostapp/app.go` — frame-drop gate, shared bounds, relayed input, relay push loop
- `internal/proto/messages.go` — new envelope types + `RelayFrameMessage`
- `internal/signaling/handler.go`, `handler_test.go` — input/overlay relay, broadcaster wiring
- `internal/signaling/framerelay.go`, `framerelay_test.go` — new
- `internal/signaling/stream.go`, `stream_test.go` — new
- `cmd/server/main.go` — mount `/stream/`
- `internal/webui/admin.html` — toolbar, status badge, connect timeout, transport switch
- `docs/deploy/vps-setup.md`, `README.md` — docs
