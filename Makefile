WAILS := go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0

.PHONY: dev build frontend test

dev:
	$(WAILS) dev

build:
	$(WAILS) build

frontend:
	cd frontend && npm ci && npm run build

test:
	go test ./internal/...
