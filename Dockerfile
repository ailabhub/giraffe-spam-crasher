FROM golang:1.26.10-alpine3.24 AS builder

ENV GOTOOLCHAIN=local

WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the application
RUN test "$(go env GOVERSION)" = go1.26.10 \
    && go build -o bot ./cmd/bot \
    && go version -m bot

FROM alpine:3.24.3
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/bot .
CMD ["./bot"]
