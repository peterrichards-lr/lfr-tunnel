# Build stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X lfr-tunnel/pkg/config.Version=${VERSION}" -o lfr-tunneld ./cmd/lfr-tunneld

# Run stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/lfr-tunneld .
ENTRYPOINT ["./lfr-tunneld"]
