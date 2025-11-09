FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY . .

WORKDIR /app/packages/relay/cmd/signalling
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /signalling

FROM alpine:3.17

WORKDIR /app

COPY --from=builder /signalling /app/signalling
COPY --from=builder /app/packages/relay/asset /app/asset

EXPOSE 8000
EXPOSE 13478
EXPOSE 16000-65535/udp

CMD ["/app/signalling"]
