FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /build/voice-proxy .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /build/voice-proxy /app/voice-proxy
USER app
EXPOSE 9081
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://localhost:9081/health || exit 1
ENTRYPOINT ["/app/voice-proxy"]
