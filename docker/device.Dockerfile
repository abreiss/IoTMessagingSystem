# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o device ./cmd/device

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Create keys directory
RUN mkdir -p /app/keys

COPY --from=builder /build/device .

CMD ["./device"]
