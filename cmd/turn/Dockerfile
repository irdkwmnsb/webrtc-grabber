FROM golang:1.25-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY cmd/turn ./cmd/turn

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o turn ./cmd/turn

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /build/turn .

# Run the binary
CMD ["./turn"]
