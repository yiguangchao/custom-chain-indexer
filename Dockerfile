# ---------------------------------------------------------
# Builder
# ---------------------------------------------------------
FROM golang:1.21-alpine AS builder

# Set up domestic agent
ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /app

# Copy the dependency files first and utilize the Docker caching layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Compile a binary file named 'indexer'
RUN go build -o indexer .

# ---------------------------------------------------------
# Runner
# ---------------------------------------------------------
FROM alpine:latest

WORKDIR /app

# Copy the binary files from the construction phase
COPY --from=builder /app/indexer .

# Expose API ports
EXPOSE 8080

# Startup command
CMD ["./indexer"]