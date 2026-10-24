# Build stage
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 go build -o puzzlebox ./cmd/server

# Runtime stage (the UI is embedded in the binary)
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /app/puzzlebox .

RUN mkdir -p data

ENV HOST=0.0.0.0 PORT=9100 BOX_DB=/app/data/box.db

EXPOSE 9100

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:9100/healthz >/dev/null || exit 1

CMD ["./puzzlebox"]
