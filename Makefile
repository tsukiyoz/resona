WAILS := go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0

.PHONY: dev build frontend test test-protocol

dev:
	$(WAILS) dev

build:
	$(WAILS) build

frontend:
	cd frontend && npm ci && npm run build

test:
	go test ./internal/...

test-protocol:
	cd third_party/teamspeak-go && go test -race ./...
