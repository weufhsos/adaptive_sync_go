# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy the main module
COPY go.mod go.sum ./
COPY *.go ./
COPY dispatcher/ dispatcher/
COPY oca/ oca/
COPY pi/ pi/
COPY proto/ proto/
COPY store/ store/
COPY transport/ transport/

# Copy simulation mock-sdn
COPY mock-sdn/ simulation/mock-sdn/

WORKDIR /app/simulation/mock-sdn

# Download dependencies
RUN go mod download

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o /mock-sdn .

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /mock-sdn .

# Default environment variables
ENV NODE_ID=sdn-1
ENV GRPC_PORT=50051
ENV HTTP_PORT=8080
ENV PEERS=""
ENV TARGET_PHI=1.05
ENV LINK_CAPACITY=1000

EXPOSE 8080 50051

CMD ["./mock-sdn"]
