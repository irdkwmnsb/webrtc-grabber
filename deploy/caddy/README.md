# Caddy reverse proxy for signalling (HTTPS + WSS)

This sets up [Caddy](https://caddyserver.com/) as a TLS-terminating reverse proxy in
front of the webrtc-grabber **signalling** server, so the admin UI, REST API, and
WebSocket signalling are served over `https://` / `wss://` with an automatically
issued & renewed Let's Encrypt certificate.

## What goes through Caddy and what doesn't

| Traffic | Path | Through Caddy? |
|---|---|---|
| Admin UI, REST API (`/api/*`), static assets | TCP `:13478` | **Yes** — proxied, HTTPS |
| WebSocket signalling (`/ws`, `/ws/player/*`, `/ws/peers/*`) | TCP `:13478` | **Yes** — proxied, WSS |
| WebRTC media (audio/video) | UDP `10000-20000` | **No** — peer-to-peer, direct to relay |

WebRTC media is encrypted end-to-end with DTLS-SRTP and travels directly between
peers and the relay over UDP. It must **not** be proxied. Open those UDP ports on
the firewall straight to the relay host and set `publicIp` in `server.yaml`.

## Setup

1. Point a DNS A/AAAA record for your domain at this host.
2. Edit `Caddyfile`: replace `signalling.example.com` with your domain (and,
   optionally, set your `email` in the global options block at the top to get
   cert-expiry notices).
3. Configure the relay (`packages/relay/conf`):
   - `server.yaml`: `port: 13478`, `publicIp: "<public ip>"`
   - `webrtc.yaml`: `portMin: 10000`, `portMax: 20000`
   - `security.yaml`: leave `tlsCrtFile`/`tlsKeyFile` **null** — Caddy handles TLS,
     the relay stays plain HTTP on loopback behind it.
4. Start Caddy:
   - Docker: `docker compose up -d` (from this directory)
   - Binary: `caddy run --config ./Caddyfile`

## Firewall

- `TCP 80, 443` → this host (ACME challenge + HTTPS/WSS)
- `UDP 10000-20000` → relay host directly (WebRTC media)

## Local testing without a domain

Use the commented `localhost { ... }` block in the `Caddyfile`. Run `caddy trust`
to install Caddy's local CA so the browser accepts the self-signed cert.
