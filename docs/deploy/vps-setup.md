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
ExecStart=/opt/rdp-tool/rdp-server -addr :9000 -ice-servers stun:127.0.0.1:3478,turn:127.0.0.1:3478 -turn-username rdp -turn-credential CHANGE_ME
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
```

Enable it (Debian/Ubuntu ships coturn disabled by default):

```bash
sudo sed -i 's/#TURNSERVER_ENABLED=1/TURNSERVER_ENABLED=1/' /etc/default/coturn
sudo systemctl enable --now coturn
sudo systemctl status coturn       # expect: active (running)
```

Open the port on your firewall/security group: UDP+TCP 3478, plus coturn's relay range (default UDP 49152-65535) if you're behind a restrictive cloud firewall.

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
rdp-host.exe -name "MY-PC" -server wss://your-domain.example/ws/host -ice-servers stun:your-domain.example:3478,turn:your-domain.example:3478 -turn-username rdp -turn-credential CHANGE_ME
```

The admin UI needs no separate configuration — it fetches its ICE server list from `https://your-domain.example/config`, which `rdp-server` already serves using the `-ice-servers`/`-turn-username`/`-turn-credential` flags from step 2.

## 6. Smoke test

```bash
systemctl status rdp-server coturn caddy    # all active (running)
```

Then from a browser on a *different network* than the Host (e.g. phone on mobile data), open `https://your-domain.example/`, confirm the Host's session appears, click it, and confirm video/mouse/keyboard/overlay-message all work — this exercises the real NAT-traversal path that `localhost` testing can't.
