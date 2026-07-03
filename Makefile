.PHONY: server host

server:
	GOOS=linux GOARCH=amd64 go build -o bin/rdp-server ./cmd/server

host:
	GOOS=windows GOARCH=amd64 go build -o bin/rdp-host.exe ./cmd/host
