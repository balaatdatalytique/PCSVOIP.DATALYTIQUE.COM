# ---- Build Stage ----
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

# Cache Go module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build a statically-linked binary (buildvcs disabled — we build from a bind mount with no .git)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w" -o /build/webserver ./cmd/server/

# ---- Runtime Stage ----
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Create a non-root user
RUN addgroup -S pcsvoip && adduser -S pcsvoip -G pcsvoip

WORKDIR /app

# Copy the compiled binary from the build stage
COPY --from=builder /build/webserver /app/webserver

# Copy website content and templates
COPY *.html /app/
COPY assets/ /app/assets/
COPY web/ /app/web/
COPY blog/ /app/blog/
COPY mail.php /app/

# Create directories for runtime data (admin bbolt DB lives in /app/data)
RUN mkdir -p /app/.cms-backups /app/data && chown -R pcsvoip:pcsvoip /app

USER pcsvoip

EXPOSE 9080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:9080/ || exit 1

ENTRYPOINT ["/app/webserver"]
CMD ["-port", "9080", "-contentDir", "/app"]
