FROM golang:1.25.1-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o nats-load-tester ./cmd/load-tester

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/nats-load-tester .

EXPOSE 9481

ENTRYPOINT ["./nats-load-tester"]
