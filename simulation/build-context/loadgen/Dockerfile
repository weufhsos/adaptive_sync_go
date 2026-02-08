# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy the main module dependencies
COPY go.mod go.sum ./

# Copy loadgen module
COPY loadgen/ ./

# Download dependencies
RUN go mod download

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /loadgen .

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /loadgen .

# Default environment variables
ENV TARGET_NODES="sdn-1:8080"
ENV RPS=10
ENV DURATION=60s
ENV PATTERN=uniform
ENV MIN_BANDWIDTH=10
ENV MAX_BANDWIDTH=100
ENV MIN_HOLD_TIME=1s
ENV MAX_HOLD_TIME=10s

CMD ["./loadgen"]
