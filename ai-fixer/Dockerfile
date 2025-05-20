# Build stage
FROM golang:1.24 as builder

# Set work directory
WORKDIR /app

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the Go app (statically linked, small binary)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ai-fixer main.go

# Final minimal image
FROM alpine:3.19

# Install CA certificates (needed for HTTPS to Kube API)
RUN apk add --no-cache ca-certificates

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/ai-fixer .

# Run the binary
ENTRYPOINT ["./ai-fixer"]
