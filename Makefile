.PHONY: server host host-linux host-linux-package

# Override any of these to bake real values into the binary, e.g.:
#   make host SERVER=wss://mydomain.com/ws/host ICE_SERVERS=stun:mydomain.com:3478,turn:mydomain.com:3478 TURN_USERNAME=rdp TURN_CREDENTIAL=secret
# The resulting binary then needs zero flags at runtime — passing a flag
# still overrides the baked-in value. Anything left unset keeps the
# Go source's own default (localhost, no TURN) instead of being
# overwritten with an empty string.
SERVER ?=
ADDR ?=
ICE_SERVERS ?=
TURN_USERNAME ?=
TURN_CREDENTIAL ?=

LDFLAGS_SERVER :=
ifneq ($(ADDR),)
LDFLAGS_SERVER += -X main.defaultAddr=$(ADDR)
endif
ifneq ($(ICE_SERVERS),)
LDFLAGS_SERVER += -X main.defaultICEServers=$(ICE_SERVERS)
endif
ifneq ($(TURN_USERNAME),)
LDFLAGS_SERVER += -X main.defaultTURNUsername=$(TURN_USERNAME)
endif
ifneq ($(TURN_CREDENTIAL),)
LDFLAGS_SERVER += -X main.defaultTURNCredential=$(TURN_CREDENTIAL)
endif

# -X flags shared by both host targets; -H windowsgui (below) is a
# PE/Windows-only linker flag and must not be passed when building for
# linux, so it's kept out of this shared list.
LDFLAGS_HOST_COMMON :=
ifneq ($(SERVER),)
LDFLAGS_HOST_COMMON += -X main.defaultServer=$(SERVER)
endif
ifneq ($(ICE_SERVERS),)
LDFLAGS_HOST_COMMON += -X main.defaultICEServers=$(ICE_SERVERS)
endif
ifneq ($(TURN_USERNAME),)
LDFLAGS_HOST_COMMON += -X main.defaultTURNUsername=$(TURN_USERNAME)
endif
ifneq ($(TURN_CREDENTIAL),)
LDFLAGS_HOST_COMMON += -X main.defaultTURNCredential=$(TURN_CREDENTIAL)
endif

LDFLAGS_HOST := -H windowsgui $(LDFLAGS_HOST_COMMON)

server:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS_SERVER)" -o bin/rdp-server ./cmd/server

host:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS_HOST)" -o bin/rdp-host.exe ./cmd/host

host-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS_HOST_COMMON)" -o bin/rdp-host-linux ./cmd/host

# Double-click launcher: two files (binary + RDP-Host.desktop) meant to
# stay side by side, but with no hardcoded path — the .desktop's Exec
# searches $HOME, common USB automount roots (/media, /run/media, /mnt)
# and the launch directory for rdp-host-linux by name, then copies
# whichever copy it finds first into $TMPDIR and runs that copy instead of
# the original. Two reasons for the copy step: the executable bit doesn't
# need to survive the file transfer, and a flash drive mounted noexec
# (common for FAT/NTFS removable media, and what actually blocks execution
# — not the filesystem type itself) can't block it, since nothing is ever
# executed directly off the drive. Type=Application is what makes
# Nautilus/Caja launch the .desktop directly on double-click instead of
# treating it as a text file, unlike a plain .sh — confirmed working by
# hand.
host-linux-package: host-linux
	rm -rf bin/rdp-host-pkg
	mkdir -p bin/rdp-host-pkg/rdp-host
	cp bin/rdp-host-linux bin/rdp-host-pkg/rdp-host/
	cp packaging/linux/RDP-Host.desktop bin/rdp-host-pkg/rdp-host/
	chmod +x bin/rdp-host-pkg/rdp-host/rdp-host-linux bin/rdp-host-pkg/rdp-host/RDP-Host.desktop
	tar -czf bin/rdp-host-linux.tar.gz -C bin/rdp-host-pkg rdp-host
	rm -rf bin/rdp-host-pkg
