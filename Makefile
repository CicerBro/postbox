.PHONY: build frontend test race bench release
frontend:
	cd web && bun install --frozen-lockfile && bun run build
build: frontend
	CGO_ENABLED=0 go build -trimpath -o bin/postbox ./cmd/postbox
test:
	go test ./...
race:
	go test -race ./...
bench:
	go test -run '^$$' -bench . -benchmem ./internal/render ./internal/send
release: frontend
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/postbox-linux-amd64 ./cmd/postbox
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/postbox-linux-arm64 ./cmd/postbox
