# --- Stage 1: Build Binaries ---
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build both API and Worker binaries
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /bin/worker ./cmd/worker

# --- Stage 2: Minimal Runtime ---
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /root/

# Copy binaries from builder
COPY --from=builder /bin/api ./api
COPY --from=builder /bin/worker ./worker

# Default command (overridden in docker-compose)
CMD ["./api"]