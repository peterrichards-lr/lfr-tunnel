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
# Portal V2 @imports files that are shared with Portal V1 and live with the static assets the
# Go server embeds -- the theme tokens (#1522) and the accessibility component rules (#1520).
# This stage copies only ui/, so without them the build fails with "Unable to resolve
# @import", which a local build never sees because there the whole repo is on disk.
#
# The destination has to mirror the repo layout, since the imports are relative paths.
#
# The whole static/ directory rather than the specific subdirectories: naming them meant this
# line had to be remembered every time something new was shared, and it was not -- #1522 added
# themes/ and #1520 then broke the image again with shared/. The cost is that editing
# dashboard.js now invalidates this layer and re-runs the UI build; 428K of context is a
# cheaper problem than a build that only fails in Docker.
COPY pkg/server/static /app/pkg/server/static
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
