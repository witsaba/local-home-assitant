# syntax=docker/dockerfile:1.7
#
# workers runtime image.
#
# Build context (per docker-compose.yml): ./services/workers
# Build stage: golang:1.26.3-alpine3.23 — same base as messaging-core.
# Runtime stage: alpine:3.23 — same as messaging-core for consistency.
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
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}-${GIT_HEAD}" \
        -o /out/workers \
        ./cmd/workers

# ---- runtime stage --------------------------------------------------
FROM alpine:3.23
RUN apk add --no-cache ca-certificates

COPY --from=build /out/workers /usr/local/bin/workers

# Run as the unprivileged 'nobody' user (uid 65534 in alpine).
USER nobody:nobody

# workers does not listen on a port in v1 — the discovery job only
# makes outbound HTTP probes. EXPOSE is intentionally omitted.
ENTRYPOINT ["/usr/local/bin/workers"]