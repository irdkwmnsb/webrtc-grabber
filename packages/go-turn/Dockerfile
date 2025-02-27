FROM golang:1.19-alpine as builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /go-turn

FROM alpine:3.17
WORKDIR /
COPY --from=builder /go-turn /go-turn
ENV PUBLIC_IP=""
ENV PORT="3478"
ENV USERS="admin=credential"
ENV REALM="nef.turn"
ENV UDP_PORT_FROM="40000"
ENV UDP_PORT_TO="40199"

EXPOSE 3478/tcp
EXPOSE 3478/udp
EXPOSE 40000-40199/udp

ENTRYPOINT ["/go-turn"]
CMD -public-ip=${PUBLIC_IP} -port=${PORT} -users=${USERS} -realm=${REALM} -udp-port-from=${UDP_PORT_FROM} -udp-port-to=${UDP_PORT_TO}