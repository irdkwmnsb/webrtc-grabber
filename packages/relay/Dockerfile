FROM golang:1.26-trixie AS builder

WORKDIR /app
COPY . .

WORKDIR /app/cmd/signalling
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /signalling

FROM alpine:3.17

WORKDIR /app

# Web assets are embedded in the binary (see package asset); no asset dir needed.
COPY --from=builder /signalling /app/signalling

EXPOSE 8000
EXPOSE 13478
EXPOSE 16000-65535/udp

CMD ["/app/signalling"]
