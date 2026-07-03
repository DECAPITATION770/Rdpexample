# VPS deployment: signaling server + TURN/STUN

This sets up the two services that must run on your VPS: `rdp-server`
(signaling relay + the admin web UI) and `coturn` (STUN/TURN, needed for
the Host and the admin browser to find each other across NAT/firewalls —
skip it only if both sides are always on the same LAN).

Assumes a Debian/Ubuntu VPS with a public IP and a domain name pointed at
it (a domain is required for the TLS step; you can skip TLS and use plain
`http://`/`ws://` for a first smoke test, but browsers will refuse mixed
content and some NATs need `wss://` in practice).

## 1. Build and copy `rdp-server`

From your dev machine:

```bash
make server                      # produces bin/rdp-server (linux/amd64)
scp bin/rdp-server you@vps:/opt/rdp-tool/rdp-server
```

## 2. Run `rdp-server` as a systemd service

On the VPS, `/etc/systemd/system/rdp-server.service`:

```ini
[Unit]
Description=RDP-Tool signaling server + admin UI
After=network.target

[Service]
ExecStart=/opt/rdp-tool/rdp-server -addr :9000 -ice-servers stun:127.0.0.1:3478,turn:127.0.0.1:3478,turns:127.0.0.1:443?transport=tcp -turn-username rdp -turn-credential CHANGE_ME
Restart=on-failure
User=rdp-tool

[Install]
WantedBy=multi-user.target
```

Use a dedicated `rdp-tool` system user (`useradd -r -s /usr/sbin/nologin rdp-tool`) rather than root. Replace `CHANGE_ME` with the same password you set for coturn in step 3.

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now rdp-server
sudo systemctl status rdp-server   # expect: active (running)
```

## 3. Install and configure coturn

```bash
sudo apt install coturn
```

`/etc/turnserver.conf` (minimal):

```
listening-port=3478
fingerprint
lt-cred-mech
user=rdp:CHANGE_ME
realm=your-domain.example
tls-listening-port=443
cert=/etc/letsencrypt/live/your-domain.example/fullchain.pem
pkey=/etc/letsencrypt/live/your-domain.example/privkey.pem
```

The `tls-listening-port` block adds a TURN-over-TLS/TCP relay on port 443.
This matters because corporate and public-wifi firewalls that block UDP
outright (blocking UDP/3478 along with it) almost always allow outbound
TCP/443, since it's indistinguishable from ordinary HTTPS traffic. Without
it, ICE has no fallback candidate on a UDP-blocked network — the Host and
viewer never establish a relay path and the viewer gets a silent black
screen with no indication of why.

Enable it (Debian/Ubuntu ships coturn disabled by default):

```bash
sudo sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn
sudo systemctl enable --now coturn
sudo systemctl status coturn       # expect: active (running)
```

Open the port on your firewall/security group: UDP+TCP 3478, plus coturn's relay range (default UDP 49152-65535) if you're behind a restrictive cloud firewall. Also open **TCP 443** to coturn for the TLS relay listener above — this is a separate requirement from the port 443 your web server (e.g. Caddy, step 4) already listens on, so if 443 is already bound by another process on this host, either give coturn a different public IP for its TLS listener or pick a different port (e.g. `tls-listening-port=5349`, the IANA-assigned TURNS port) and pass that port in the `turns:` URL below instead. The UDP/3478 path from this step remains the primary transport either way; TCP/443 (or whatever port you chose) is only a fallback for networks that block UDP.

## 4. Put TLS in front of rdp-server (recommended)

Simplest option is Caddy, which handles Let's Encrypt automatically. `/etc/caddy/Caddyfile`:

```
your-domain.example {
	reverse_proxy localhost:9000
}
```

```bash
sudo apt install caddy
sudo systemctl restart caddy
```

This gets you `https://your-domain.example/` for the admin UI and `wss://your-domain.example/ws/...` for signaling — both `cmd/host -server` and the admin browser page should use these `wss://`/`https://` URLs, not the raw `:9000` port, once this is in place.

## 5. Point the Host at the deployed server

```powershell
rdp-host.exe -name "MY-PC" -server wss://your-domain.example/ws/host -ice-servers stun:your-domain.example:3478,turn:your-domain.example:3478,turns:your-domain.example:443?transport=tcp -turn-username rdp -turn-credential CHANGE_ME
```

The `turns:...?transport=tcp` entry is the TCP/443 fallback from step 3 — pion (on the Host) and the browser (via `/config`, below) both receive the full comma-separated list and try every candidate, so this is additive: UDP/3478 is still tried first and used whenever it's reachable.

The admin UI needs no separate configuration — it fetches its ICE server list from `https://your-domain.example/config`, which `rdp-server` already serves using the `-ice-servers`/`-turn-username`/`-turn-credential` flags from step 2.

### The `/stream/{session_id}` HTTP fallback needs no extra firewall rules

If WebRTC fails to connect (or the admin clicks the ⇄ toggle), the
viewer falls back to `GET /stream/{session_id}`, a plain
`multipart/x-mixed-replace` (MJPEG) feed served by the same
`rdp-server` process on the same host/port as the admin UI itself.
Mouse/keyboard/overlay-message input in this mode rides the existing
signaling WebSocket instead of a WebRTC DataChannel. Because it's
just another HTTP(S) route on the port you already opened for the
admin page (step 4, or `:9000` directly), it needs zero additional
firewall/security-group configuration — no UDP, no TURN, nothing
beyond what already lets `https://your-domain.example/` load. This is
the point of the fallback: it works even on networks that block UDP
outright and where the TURN-over-TCP/443 path below still somehow
doesn't get through.

## 6. Smoke test

```bash
systemctl status rdp-server coturn caddy    # all active (running)
```

Then from a browser on a *different network* than the Host (e.g. phone on mobile data), open `https://your-domain.example/`, confirm the Host's session appears, click it, and confirm video/mouse/keyboard/overlay-message all work — this exercises the real NAT-traversal path that `localhost` testing can't.

### Verifying the TURN-over-TCP/443 fallback

There's no automated test for this — it's infra, and the whole point is
exercising the codepath a normal LAN/UDP-friendly test never touches. To
verify it manually: from a network that blocks outbound UDP (a locked-down
corporate network, or a laptop with an outbound UDP firewall rule added
temporarily), open `chrome://webrtc-internals` in a second tab, start a
session against the Host, and watch the `PeerConnection` entry that
appears. Confirm `iceConnectionState` reaches `connected` (not stuck at
`checking` or `failed`) and that the selected candidate pair's local or
remote candidate type is `relay` — that's the TURN/TCP/443 path doing the
work, since a UDP-blocked network can never produce a `srflx` or `host`
candidate pair.
