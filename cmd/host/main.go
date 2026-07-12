//go:build windows || linux

package main

import (
	"flag"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rdpAiAnswer/internal/hostapp"
)

// These are the compiled-in defaults. Override them at build time with
// -ldflags, e.g.:
//
//	go build -ldflags "-X main.defaultServer=wss://mydomain.com/ws/host -X main.defaultICEServers=stun:mydomain.com:3478,turn:mydomain.com:3478 -X main.defaultTURNUsername=rdp -X main.defaultTURNCredential=secret" -o rdp-host.exe ./cmd/host
//
// (the Makefile's `host` target does this for you — see `make host SERVER=... ICE_SERVERS=...`).
// The resulting .exe then runs with zero flags needed on the target PC;
// passing a flag at runtime still overrides the baked-in value.
var (
	defaultServer         = "ws://localhost:9000/ws/host"
	defaultICEServers     = ""
	defaultTURNUsername   = ""
	defaultTURNCredential = ""
)

// openLogFile opens (creating if needed) a log file next to the running
// executable. Built with -H windowsgui (see the Makefile's `host`
// target) so double-clicking rdp-host.exe never pops up a console
// window — but that also means log.Printf output has nowhere visible to
// go by default. Writing to a file next to the exe keeps it inspectable
// without requiring a console.
//
// If that location isn't writable — rdp-host.exe run from Program
// Files, a locked-down folder, or a read-only mount, all of which deny
// a normal user write access — falls back to the OS temp directory,
// which is always writable for the current user session. Silently
// giving up here (the previous behavior) meant a permission error left
// nothing logged anywhere and nothing visible to say why.
func openLogFile() (*os.File, error) {
	const logName = "rdp-host.log"

	if exePath, err := os.Executable(); err == nil {
		logPath := filepath.Join(filepath.Dir(exePath), logName)
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			return f, nil
		}
	}

	logPath := filepath.Join(os.TempDir(), logName)
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

func main() {
	if logFile, err := openLogFile(); err == nil {
		defer logFile.Close()
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}
	// If the log file couldn't be opened, log still falls back to its
	// default (stderr) — silently proceeding either way rather than
	// making a missing log file fatal.

	hostname, _ := os.Hostname()

	name := flag.String("name", hostname, "display name shown in the admin's session list (defaults to this PC's hostname)")
	server := flag.String("server", defaultServer, "signaling server URL")
	iceServers := flag.String("ice-servers", defaultICEServers, "comma-separated STUN/TURN URLs, e.g. stun:vps:3478,turn:vps:3478")
	turnUsername := flag.String("turn-username", defaultTURNUsername, "TURN username, if any TURN URL is passed in -ice-servers")
	turnCredential := flag.String("turn-credential", defaultTURNCredential, "TURN credential, if any TURN URL is passed in -ice-servers")
	fps := flag.Int("fps", 30, "target frames per second for the live stream (both WebRTC and HTTP fallback)")
	quality := flag.Int("quality", 75, "JPEG quality 1-100; higher is sharper but larger/slower")
	maxWidth := flag.Int("max-width", 0, "downscale captures to at most this width in pixels for speed; 0 = native resolution")
	flag.Parse()

	cfg := hostapp.Config{SignalingURL: *server, Name: *name, ICEUsername: *turnUsername, ICECredential: *turnCredential, JPEGQuality: *quality, MaxWidth: *maxWidth}
	if *iceServers != "" {
		cfg.ICEServers = strings.Split(*iceServers, ",")
	}
	if *fps > 0 {
		cfg.FrameDelay = time.Second / time.Duration(*fps)
	}

	log.Printf("connecting to %s as %q", cfg.SignalingURL, cfg.Name)
	if err := hostapp.Run(cfg); err != nil {
		log.Fatal(err)
	}
}
