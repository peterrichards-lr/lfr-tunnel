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
RUN pnpm run build

# Go Build Stage
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
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
