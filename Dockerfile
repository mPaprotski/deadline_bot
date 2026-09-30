# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS builder

# ca-certificates needed for HTTPS calls to Telegram API
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /deadline_bot ./cmd/bot

# ── Stage 2: minimal runtime ───────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

# Non-root user
RUN addgroup -S botgroup && adduser -S botuser -G botgroup

WORKDIR /app
COPY --from=builder /deadline_bot ./deadline_bot

# Data directory – will be mounted as a volume
RUN mkdir -p /data && chown botuser:botgroup /data

USER botuser

EXPOSE 8080

ENTRYPOINT ["/app/deadline_bot"]
