# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27.1
ARG BUN_VERSION=1.4.2

FROM --platform=$BUILDPLATFORM oven/bun:${BUN_VERSION} AS frontend
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd/postbox/ ./cmd/postbox/
COPY internal/ ./internal/
COPY --from=frontend /src/internal/web/dist/ ./internal/web/dist/
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/postbox ./cmd/postbox
# Seed the named volume with a private directory owned by the runtime user.
RUN mkdir -p /out/data && chmod 0700 /out/data && chown 65532:65532 /out/data

FROM scratch AS runtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/postbox /usr/local/bin/postbox
COPY --from=build --chown=65532:65532 /out/data/ /var/lib/postbox/
ENV HOME=/var/lib/postbox \
    POSTBOX_DB=/var/lib/postbox/postbox.db \
    POSTBOX_SECRET_FILE=/var/lib/postbox/tracking.secret \
    POSTBOX_BACKUP_DIR=/var/lib/postbox/backups \
    POSTBOX_LISTEN=0.0.0.0:8080
WORKDIR /var/lib/postbox
USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/postbox"]
CMD ["serve"]
