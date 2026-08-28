# UI Build Stage
FROM node:20-alpine AS ui-builder
WORKDIR /app
# First just copy package manifests
COPY ui/package.json ui/pnpm-lock.yaml* ./ui/
# Change to ui dir and install pnpm
WORKDIR /app/ui
RUN npm install -g pnpm && pnpm install
# Copy the rest of the UI files and build
COPY ui/ ./
# The theme tokens are shared with Portal V1 and live with the static assets the Go server
# embeds, so ui/src/themes/index.css @imports them from outside ui/ (#1522). This stage copies
# only ui/, so without this the build fails with "Unable to resolve @import" -- which a local
# build never sees, because there the whole repo is on disk.
#
# The destination has to mirror the repo layout, since the import is a relative path.
COPY pkg/server/static/themes /app/pkg/server/static/themes
RUN pnpm run build

# Go Build Stage
FROM golang:1.26-alpine AS builder
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
# Copy UI dist to where go:embed expects it
COPY --from=ui-builder /app/ui/dist ./pkg/server/ui-dist
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=${VERSION}" -o lfr-tunneld ./cmd/lfr-tunneld

# Run stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/lfr-tunneld .
ENTRYPOINT ["./lfr-tunneld"]
