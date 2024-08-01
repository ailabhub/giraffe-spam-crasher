FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o bot ./cmd/bot

FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/bot .
CMD ["./bot"]