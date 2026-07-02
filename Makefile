.PHONY: server host viewer viewer-cross

server:
	GOOS=linux GOARCH=amd64 go build -o bin/rdp-server ./cmd/server

host:
	GOOS=windows GOARCH=amd64 go build -o bin/rdp-host.exe ./cmd/host

# viewer uses Fyne (cgo) — plain cross-compile will fail on macOS without a
# mingw toolchain. Use fyne-cross (Docker) instead:
viewer-cross:
	go run github.com/fyne-io/fyne-cross@latest windows -arch=amd64 -app-id=com.rdpaianswer.viewer ./cmd/viewer

viewer:
	go build -o bin/rdp-viewer ./cmd/viewer
