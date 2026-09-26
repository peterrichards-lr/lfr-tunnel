# The E2E stack's WebSocket target (#2249).
#
# Build context is this directory, not the repository root: the package is stdlib-only and its
# module has no requirements, so there is nothing to download and nothing else to copy. That is
# the point -- the job this fixture exists to make useful is already the most expensive in CI,
# and both base images here are ones the lfr-tunnel and lfr-tunneld services have already pulled.
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -o /ws-echo .

FROM alpine:3.22
RUN adduser -D -u 10001 -h /home/lfr lfr
USER 10001:10001
COPY --from=builder /ws-echo /usr/local/bin/ws-echo
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/ws-echo"]
