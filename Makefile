.PHONY: build frontend test test-browser race bench release security security-go security-frontend
GOVULNCHECK_VERSION := v1.8.0
frontend:
	cd web && bun install --frozen-lockfile && bun run build
build: frontend
	CGO_ENABLED=0 go build -trimpath -o bin/postbox ./cmd/postbox
server:
	CGO_ENABLED=0 go build -trimpath -o bin/postbox ./cmd/postbox
test:
	cd web && bun test
	go test ./...
	$(MAKE) test-browser
test-browser:
	cd web && bun run test:browser
race:
	go test -race ./...
bench:
	go test -run '^$$' -bench . -benchmem ./internal/render ./internal/send
security: security-go security-frontend
security-go:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...
security-frontend:
	cd web && bun audit
release: frontend
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/postbox-linux-amd64 ./cmd/postbox
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/postbox-linux-arm64 ./cmd/postbox
