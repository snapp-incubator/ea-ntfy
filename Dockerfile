# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /build

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o ea-ntfy .

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.19

COPY --from=builder /build/ea-ntfy /ea-ntfy
COPY --from=builder /build/templates /templates

RUN apk add --no-cache ca-certificates wget

EXPOSE 8080

ENTRYPOINT ["/ea-ntfy"]
