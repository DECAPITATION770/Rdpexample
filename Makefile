.PHONY: server host host-linux

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
