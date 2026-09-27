# syntax=docker/dockerfile:1.7
#
# messaging-core runtime image.
#
# Build context (per docker-compose.yml): ./services/messaging-core
# Build stage: golang:1.26.3-alpine3.23 — verified 66.36 MB compressed
#   on Docker Hub, smallest official Go 1.26 image. musl matches the
#   runtime stage so CGO_ENABLED=0 binaries are drop-in.
# Runtime stage: alpine:3.23 — small, has a shell and apk for debug
#   (`docker exec`); production deployments can swap to scratch once
#   nothing else is bundled in.
#
# Version stamping: VERSION and GIT_HEAD are exposed as build args.
# The compose file passes them from env (defaults 0.1.0-dev / unknown),
# matching the existing Makefile's LDFLAGS scheme.

# ---- build stage ----------------------------------------------------
FROM golang:1.26.3-alpine3.23 AS build
WORKDIR /src

# Cache modules separately so source changes don't bust the layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=0.1.0-dev
ARG GIT_HEAD=unknown

# Static, stripped, version-stamped, linux target.
#   -trimpath  : drop absolute paths from binary (reproducible, smaller)
#   -s -w      : strip debug info
#   main.version stamped to "<VERSION>-<GIT_HEAD>" to match Makefile.
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}-${GIT_HEAD}" \
        -o /out/messaging-core \
        ./cmd/messaging-core

# ---- runtime stage --------------------------------------------------
FROM alpine:3.23
RUN apk add --no-cache ca-certificates

COPY --from=build /out/messaging-core /usr/local/bin/messaging-core

# Run as the unprivileged 'nobody' user (uid 65534 in alpine).
USER nobody:nobody

# NATS default port. EXPOSE is metadata only — with network_mode: host
# in compose the actual bind happens on the host's loopback.
EXPOSE 4222

ENTRYPOINT ["/usr/local/bin/messaging-core"]