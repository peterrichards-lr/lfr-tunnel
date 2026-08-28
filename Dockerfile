# Client image. Builds ./cmd/lfr-tunnel only.
#
# There is deliberately no UI build stage here (#1330). This image builds the *client*, and
# `go list -deps ./cmd/lfr-tunnel` resolves to pkg/config, pkg/client, pkg/gui, pkg/mcp and
# pkg/osutil -- pkg/server is not in the graph, so its `//go:embed ui-dist` never runs and a
# copied bundle is discarded. The stage was fallout from #1196, which correctly added the UI
# build to CI, the release workflow and the server image; this one never needed it.
#
# The server image is cmd/lfr-tunneld/Dockerfile, and that is where the UI stage belongs.

# Go Build Stage
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
# Retried, because a transient proxy.golang.org error used to fail the whole build (#1505).
# Twice on master in one day the module proxy dropped an HTTP/2 stream mid-fetch
# ("INTERNAL_ERROR; received from peer"), leaving a required check red and needing a manual
# re-run. Retrying is safe by construction: this fetches immutable, checksum-verified content,
# and go.sum means a corrupted or substituted response fails the build rather than being
# retried into acceptance.
#
# The final attempt runs OUTSIDE the loop so its failure is the build's failure. A loop that
# swallowed the last error would exit 0 with no modules and fail confusingly at `go build`
# instead -- a late, misleading error in place of an early, accurate one.
RUN for i in 1 2; do \
        go mod download && exit 0; \
        echo "go mod download failed (attempt $i), retrying..."; \
        sleep 5; \
    done; \
    go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=${VERSION}" -o lfr-tunnel ./cmd/lfr-tunnel

# Run stage
#
# Pinned rather than :latest, so a client image built today and one built next month are the
# same image. Bump it deliberately.
FROM alpine:3.22

# The client needs no privileges: it opens outbound connections and writes nothing outside its
# own home. Running as root was inherited from the default, not chosen.
#
# A numeric UID is used in USER so an orchestrator can enforce runAsNonRoot -- it cannot resolve
# a name against this image's /etc/passwd.
RUN apk --no-cache add ca-certificates \
    && adduser -D -u 10001 -h /home/lfr lfr
USER 10001:10001
WORKDIR /home/lfr
COPY --from=builder --chown=10001:10001 /app/lfr-tunnel .
ENTRYPOINT ["./lfr-tunnel"]
