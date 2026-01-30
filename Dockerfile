# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o dra-file-driver ./cmd/dra-driver

# Runtime stage
FROM alpine:3.21

RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /app/dra-file-driver /dra-file-driver

ENTRYPOINT ["/dra-file-driver"]
