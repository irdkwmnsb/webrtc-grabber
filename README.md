# WebRTC-grabber

---

The main use case is streaming live screen video from contestants' screens on
[ICPC World Finals](https://icpc.global/) as a part of
[ICPC Live](https://live.icpc.global/) broadcast.

**Table of Contents**

1. [WebRTC Protocol](#webrtc-protocol)
2. [Architecture Overview](#architecture-overview)
3. [Grabber](#grabber)
   - [Configuration](#grabber-config)
   - [Build sources](#grabber-build)
   - [Run](#grabber-run)
4. [Relay Server (Signaling + SFU)](#relay-server)
   - [Configuration](#relay-config)
   - [Build sources](#relay-build)
   - [Run](#relay-run)
   - [SFU Architecture](#sfu-architecture)
   - [Code Documentation](#code-documentation)
5. [TURN](#turn)
   - [Using Docker](#turn-docker)
   - [Build sources](#turn-build)
   - [Run](#turn-run)
6. [Contributing & Support](#contributing--support)
7. [FAQ](#faq)
8. [License](#license)

## WebRTC Protocol

---

[WebRTC](https://webrtc.org/) is a modern protocol for real-time video communication and screen sharing. It provides low-latency peer-to-peer connections with on-demand stream initiation (typically under 1 second) and portable deployment across platforms.

<img src="img/webrtc-protocol.png">

In the ICPC competition environment with strict network segmentation between "blue" (participant) and "red" (production) networks, direct peer-to-peer communication is challenging. The solution uses an **SFU (Selective Forwarding Unit)** architecture with optional **TURN** relay for NAT traversal when needed.

## Architecture Overview

---

The system consists of three main components:

1. **Grabber** - Lightweight Electron application running on contestant computers that captures and streams screen + webcam
2. **Relay Server** - Combined signaling and SFU server written in Go that:
   - Handles WebRTC signaling via WebSocket
   - Acts as an SFU to distribute media streams efficiently
   - Manages peer discovery and health monitoring
   - Provides admin dashboard for monitoring
3. **TURN Server** (optional) - Relay server for NAT traversal when direct SFU connections fail
                   WebRTC (Media streams)

## Grabber

---

Grabber is an Electron application that runs in the background and captures screen/webcam streams. It connects to the relay server via WebSocket and streams media only when requested by viewers.

### Configuration <a name="grabber-config"></a>

Grabber [`config.json`](packages/grabber/config.json):

```json
{
  "webcamConstraint": {
    "aspectRatio": 1.7777777778
  },
  "webcamAudioConstraint": true,
  "desktopConstraint": {
    "width": 1280,
    "height": 720
  }
}
```

where

| Property                | Description                                       | Type        |
| ----------------------- | ------------------------------------------------- | ----------- |
| `webcamConstraint`      | Webcam constraints                                | **object**  |
| `aspectRatio`           | Source aspect ratio                               | **number**  |
| `webcamAudioConstraint` | Sets the constraints on contestant's webcam audio | **boolean** |
| `desktopConstraint`     | Constraints on screen sharing                     | **object**  |
| `width`                 | Width of the sharing screen                       | **number**  |
| `height`                | Height of the sharing screen                      | **number**  |

### Build sources <a name="grabber-build"></a>

Clone the repository and run the following commands from the project root:

#### Windows

```powershell
$ grabber_build_win64.bat
```

#### Linux & MacOS

```shell
$ sh grabber_build.sh <platform> <arch>
```

where

- `<platform>` can be one of `linux`, `win32`, `macos`;
- `<arch>` can be `x64` or `arm64`.

### Run <a name="grabber-run"></a>

On the contestants' PC you need to extract files from the
`webrtc_grabber_grabber_<platform>_<arch>.zip` archive, which you can find on
the [Release page](https://github.com/irdkwmnsb/webrtc-grabber/releases/latest).

After that, you can run the grabber using executable:

#### Windows

- Launch in background (see
  [`runner.bat`](packages/grabber/scripts/runner.bat)):

  ```
  $ ~dp0grabber.exe . --peerName={number of computer} --signalingUrl="{signalling url}"
  ```

- For testing use the
  script [`tester.bat`](packages/grabber/scripts/tester.bat):

  ```
  $ ~dp0grabber.exe . --debugMode --peerName={number of computer}
  --signalingUrl="{signalling url}"
  ```

- Stop the grabber with
  the [`stopper.bat`](packages/grabber/scripts/stopper.bat)
  script.

#### Linux

Use [`grabber-linux.sh`](packages/grabber/scripts/grabber-linux.sh) script:

- Launch in background:

  ```shell
  $ bash grabber-linux.sh run {computer number} {signalling url}
  ```

- For testing use

  ```shell
  $ bash grabber-linux.sh test {computer number} {signalling url}
  ```

- Stop the grabber with
  ```shell
  $ bash grabber-linux.sh stop {computer number} {signalling url}
  ```

#### MacOS

The same as for Linux, but name of the script is
[`grabber-darwin.sh`](packages/grabber/scripts/grabber-darwin.sh).

## Relay Server (Signaling + SFU)

---

The relay server is a high-performance Go application combining WebRTC signaling and SFU (Selective Forwarding Unit) functionality. It uses the Fiber web framework with native WebSocket support for efficient real-time communication.

### Key Features

- **Integrated SFU**: Efficiently distributes media from publishers to multiple subscribers without transcoding
- **Concurrent Broadcasting**: Uses goroutine pools with semaphore-based throttling for optimal performance
- **Automatic Cleanup**: Handles peer disconnections and stale connections gracefully
- **Health Monitoring**: Tracks grabber status via periodic pings
- **Access Control**: IP-based whitelisting and credential authentication for admin endpoints
- **Multiple Stream Types**: Supports both webcam (video+audio) and screen share (video only) per grabber

### Configuration <a name="relay-config"></a>

Relay server [`config.json`](packages/relay/conf/config.json):

```json
{
  "participants": ["team-001", "team-002", "team-003"],
  "adminsNetworks": ["127.0.0.1/32", "10.0.0.0/8", "192.168.0.0/16"],
  "adminCredential": "your-secure-password",
  "peerConnectionConfig": {
    "iceServers": [
      {
        "urls": ["stun:stun.l.google.com:19302"],
        "username": "",
        "credential": ""
      }
    ]
  },
  "grabberPingInterval": 5,
  "serverPort": 8000,
  "serverTLSCrtFile": null,
  "serverTLSKeyFile": null,
  "codecs": [
    {
      "type": "video",
      "params": {
        "mimeType": "video/VP8",
        "clockRate": 90000,
        "payloadType": 96,
        "channels": 0
      }
    },
    {
      "type": "audio",
      "params": {
        "mimeType": "audio/opus",
        "clockRate": 48000,
        "payloadType": 111,
        "channels": 2
      }
    }
  ],
  "webcamTrackCount": 2
}
```

### Configuration Parameters

| Property               | Description                                                      | Type             | Default |
| ---------------------- | ---------------------------------------------------------------- | ---------------- | ------- |
| `participants`         | List of expected grabber names for monitoring                   | **string[]**     | `[]`    |
| `adminsNetworks`       | CIDR ranges allowed to access admin/player endpoints            | **string[]**     | `[]`    |
| `adminCredential`      | Password for admin authentication (null = no auth)              | **string\|null** | `null`  |
| `peerConnectionConfig` | WebRTC peer connection configuration                            | **object**       | -       |
| `iceServers`           | STUN/TURN servers for NAT traversal                            | **object[]**     | -       |
| `grabberPingInterval`  | How often grabbers should ping (seconds)                        | **number**       | `5`     |
| `serverPort`           | HTTP/WebSocket server port                                      | **number**       | `8000`  |
| `serverTLSCrtFile`     | Path to TLS certificate for HTTPS/WSS (null = no TLS)          | **string\|null** | `null`  |
| `serverTLSKeyFile`     | Path to TLS private key for HTTPS/WSS                          | **string\|null** | `null`  |
| `codecs`               | Supported audio/video codecs (VP8, VP9, H264, Opus, etc.)      | **object[]**     | -       |
| `webcamTrackCount`     | Expected number of tracks for webcam streams (video+audio)      | **number**       | `2`     |

### Build sources <a name="relay-build"></a>

Clone the repository and run the following commands from `packages/relay`:

```shell
cd packages/relay/cmd/signaling
go mod tidy
go build -o signaling
```

For cross-compilation:

```shell
# Linux
GOOS=linux GOARCH=amd64 go build -o signaling-linux

# Windows
GOOS=windows GOARCH=amd64 go build -o signaling.exe

# macOS
GOOS=darwin GOARCH=amd64 go build -o signaling-darwin
```

### Run <a name="relay-run"></a>

Extract files from `webrtc_grabber_signaling_<platform>_<arch>.zip` from the [Release page](https://github.com/irdkwmnsb/webrtc-grabber/releases/latest).

#### Windows

```powershell
$ signalling.cmd
```

#### Linux & MacOS

```shell
$ sh signalling.sh
```

Or run directly:

```shell
$ ./signaling
```

The server will start on the configured port (default: 8000). Access the admin dashboard at `http://localhost:8000`.

### SFU Architecture <a name="sfu-architecture"></a>

The SFU implementation is built into the relay server and provides efficient one-to-many media distribution.

<img src="img/sfu.png">

#### Core Components

1. **PeerManager** (`packages/relay/internal/signalling/peer_manager.go`)
   - Orchestrates all WebRTC peer connections
   - Manages publishers (grabbers) and subscribers (players)
   - Handles concurrent publisher setup with atomic synchronization
   - Implements automatic cleanup on disconnections

2. **TrackBroadcaster** (`packages/relay/internal/signalling/track_broadcaster.go`)
   - Reads RTP packets from publisher tracks
   - Broadcasts packets to all subscribers concurrently
   - Uses semaphore-based throttling (max 20 concurrent writes)
   - Automatically removes failed subscribers

3. **Server** (`packages/relay/internal/signalling/server.go`)
   - HTTP/WebSocket server using Fiber framework
   - Routes signaling messages between grabbers and players
   - Manages three WebSocket endpoints
   - Enforces IP-based access control

4. **Storage** (`packages/relay/internal/signalling/storage.go`)
   - Thread-safe peer registry with health monitoring
   - Tracks ping timestamps and connection counts
   - Provides participant status for admin dashboard
   - Automatic cleanup of stale peers (60-second timeout)

#### Stream Types

Each grabber can provide multiple stream types simultaneously:

- **webcam**: Video + Audio from participant's webcam (configurable track count)
- **screen**: Video only from screen capture

Subscribers can request specific stream types independently.

#### Connection Flow

**Publisher (Grabber) Flow:**
1. Connects to `/ws/peers/:name` WebSocket endpoint
2. Receives `InitPeer` with WebRTC configuration
3. Sends periodic `Ping` messages with status
4. Receives `Offer` when first subscriber requests stream
5. Responds with `OfferAnswer` and ICE candidates
6. Begins streaming media tracks via WebRTC

**Subscriber (Player) Flow:**
1. Authenticates via IP whitelist + credential
2. Connects to `/ws/player/play` WebSocket endpoint
3. Sends `Offer` specifying grabber and stream type
4. Receives `OfferAnswer` from server
5. Exchanges ICE candidates
6. Receives media via WebRTC peer connection

#### Performance Optimizations

- **Concurrent Packet Distribution**: Goroutine pool with semaphore limiting
- **Memory Management**: GC tuned to 20% for low-latency streaming
- **Lock-Free Operations**: Uses `sync.Map` and atomic operations where possible
- **Lazy Publisher Setup**: Publishers created only when first subscriber connects
- **Automatic Resource Cleanup**: All resources released when last subscriber disconnects

#### Scalability

The SFU architecture eliminates the N×N connection problem of mesh topologies:

- **Without SFU (Mesh)**: N grabbers × M viewers = N×M connections
- **With SFU**: N grabbers + M viewers = N+M connections to SFU

Example: 50 grabbers, 10 viewers
- Mesh: 500 peer connections
- SFU: 60 connections total

### Code Documentation

Full API documentation is available via `go doc`:

```shell
# View package documentation
go doc github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling

# View specific type documentation
go doc signalling.PeerManager
go doc signalling.TrackBroadcaster
go doc signalling.Server

# Start HTML documentation server
godoc -http=:6060
# Then visit: http://localhost:6060/pkg/github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling/
```

## TURN

---

**⚠️ Note**: TURN servers are typically unnecessary when using the SFU architecture, as the SFU server acts as a central relay point. TURN is only needed if the SFU server itself cannot be reached directly from both grabber and player networks.

TURN server is used to transmit video/audio data across different networks when direct connections fail. We provide a lightweight Go-based TURN server implementation.

### When to Use TURN

Use TURN only if:
- SFU server is behind NAT and unreachable from grabber or player networks
- Firewall rules prevent direct UDP/TCP connections to SFU
- You need to relay traffic through a specific network boundary

For most deployments, configure the SFU server with a public IP or appropriate port forwarding instead of deploying TURN.

### Using Docker <a name="turn-docker"></a>

The easiest way to run the TURN server is using Docker:

```shell
docker run -d --network=host \
  -v $(pwd)/turn-config.json:/etc/turn-server-conf.json \
  ghcr.io/irdkwmnsb/webrtc-grabber-turn:latest
```

### Build sources <a name="turn-build"></a>

Clone the repository and run the following commands from `packages/go-turn`:

```shell
$ go mod tidy
$ go build
```

### Run <a name="turn-run"></a>

Extract files from the
`webrtc_grabber_turn_<platform>_<arch>.zip` archive, which you can find on
the [Release page](https://github.com/irdkwmnsb/webrtc-grabber/releases/latest).

After that, you can run the TURN server using scripts:

#### Windows

```powershell
$ turn.cmd
```

#### Linux & MacOS

```shell
$ sh turn.sh
```
## Contributing & Support

---

### 🐛 Found a Bug?

If you discover a bug or have a feature request, please:
1. Check if the issue already exists in [Issues](https://github.com/irdkwmnsb/webrtc-grabber/issues)
2. If not, create a new issue with:
   - Clear description of the problem
   - Steps to reproduce
   - Expected vs actual behavior
   - Environment details (OS, Go version, etc.)
   - Relevant logs

### 💡 Want to Contribute?

Contributions are welcome! Here's how you can help:
- **Code**: Submit pull requests for bug fixes or new features
- **Documentation**: Improve README, add examples, fix typos
- **Testing**: Test in different environments and report results
- **Ideas**: Share suggestions for improvements

### 📧 Contact

- **GitHub**: [@Mond1c](https://github.com/Mond1c)
- **Issues**: [github.com/irdkwmnsb/webrtc-grabber/issues](https://github.com/irdkwmnsb/webrtc-grabber/issues)
- **Telegram**: [@Mond1c](https://t.me/Mond1c)

### 🌟 Project Status

This project is under active development. The relay server (Go/SFU implementation) represents a complete rewrite from the original P2P version, bringing improved performance, reliability, and scalability.

**Current focus areas:**
- Performance optimization and stress testing
- Enhanced monitoring and debugging tools
- Improved documentation and deployment guides
- Additional codec support

If you're using this in production or planning to, feel free to reach out - I'd love to hear about your use case!


## FAQ

---

**General**

> **Q:** Is VLC still required on participant computers?  
> **A:** **No**. The grabber application handles all media capture.

> **Q:** Does the grabber start streaming immediately when launched?  
> **A:** **No**. It connects to the signaling server and sends periodic pings every 5 seconds (configurable), but only streams when a viewer requests it.

> **Q:** What happens when the SFU server restarts?  
> **A:** Grabbers automatically reconnect and re-register. Active viewers need to reconnect and re-request streams.

> **Q:** Can multiple viewers watch the same grabber simultaneously?  
> **A:** **Yes**. The SFU architecture efficiently distributes streams to unlimited viewers without additional load on the grabber.

> **Q:** What's the typical latency?  
> **A:** End-to-end latency is typically 200-500ms in local networks, depending on network conditions and processing delays.

> **Q:** How to test webrtc-grabber without Internet access?  
> **A:** Run the relay server on a local machine accessible to both grabbers and viewers. If all devices are on the same network without NAT, no TURN server is needed. Access the admin dashboard at `http://<server-ip>:8000`.

**Performance**

> **Q:** How much bandwidth does each stream consume?  
> **A:** Approximately 2-3 Mbps per grabber stream (depends on resolution and codec settings). With SFU, this bandwidth is only between grabber and SFU server, not multiplied by viewer count.

> **Q:** How many concurrent streams can one SFU server handle?  
> **A:** With proper hardware (4+ CPU cores, 8GB+ RAM), one SFU instance can handle 50-100 grabbers with hundreds of viewers. Performance depends on:
   - CPU cores (for concurrent packet processing)
   - Network bandwidth (not computation-bound)
   - Memory (minimal, ~50MB per grabber)

> **Q:** Does the SFU transcode video?  
> **A:** **No**. The SFU forwards RTP packets without transcoding, keeping CPU usage low and latency minimal.

> **Q:** What network bandwidth is required for the SFU server?  
> **A:** For N grabbers and M viewers: approximately N × 3 Mbps inbound + M × 3 Mbps outbound. Example: 5 grabbers, 10 viewers = ~15 Mbps in + ~30 Mbps out = 45 Mbps total.

**Security**

> **Q:** How is admin access secured?  
> **A:** Two-layer security: IP-based whitelisting (`adminsNetworks` in config) and credential authentication (`adminCredential`).

> **Q:** Are grabber endpoints secured?  
> **A:** Grabber endpoints have no authentication by design - they should be on an isolated network accessible only to trusted devices.

> **Q:** Is video encrypted?  
> **A:** **Yes**. WebRTC uses DTLS-SRTP for end-to-end encryption of media streams.

> **Q:** Can I use HTTPS/WSS instead of HTTP/WS?  
> **A:** **Yes**. Configure `serverTLSCrtFile` and `serverTLSKeyFile` in the relay server config with paths to your SSL certificate and key.

**Deployment**

> **Q:** Can I run multiple SFU instances for redundancy?  
> **A:** The current implementation is single-instance. For redundancy, use a reverse proxy (nginx, HAProxy) with health checks and failover.

> **Q:** Do I need TURN if the SFU server has a public IP?  
> **A:** Usually **no**. If both grabbers and viewers can reach the SFU server directly, TURN is unnecessary.

> **Q:** What ports need to be open in the firewall?  
> **A:** 
   - **For SFU server**: 
     - TCP port 8000 (or configured `serverPort`) for WebSocket connections
     - UDP ports for WebRTC (typically ephemeral ports 49152-65535, or configure specific range in OS)
   - **For grabbers**: Outbound connections to SFU server
   - **For viewers**: Outbound connections to SFU server
   - **For TURN** (if used): TCP/UDP port 3478, UDP port range for relay (e.g., 40000-40199)

**Connections and Network**

> **Q:** How many TCP/UDP ports are needed?  
> **A:** The SFU uses one WebSocket connection per client (grabber or viewer) on the configured port. WebRTC media uses UDP with dynamic port allocation (typically 1-2 ports per active peer connection).

> **Q:** Do all grabbers use the same server port?  
> **A:** **Yes**. All grabbers connect to the same WebSocket endpoint. They are distinguished by their socket connection ID and their configured `peerName`.

> **Q:** What delays are acceptable for normal operation?  
> **A:** WebRTC works well with latencies up to 200-300ms. The protocol handles packet loss gracefully. The system has been tested successfully over WiFi and VPN connections. Connection health is monitored automatically and will attempt recovery until explicitly closed.

> **Q:** Have you tested compatibility with OpenVPN?  
> **A:** **Yes**. Streaming over VPN works, though it may add 20-50ms of latency.

> **Q:** Is TURN-relay required at the network edge?  
> **A:** With SFU architecture, TURN is rarely needed. The SFU server should be accessible from both contestant and viewer networks. If the SFU server is on the network boundary with proper routing, TURN is unnecessary.

**Troubleshooting**

> **Q:** Grabber shows "connected" but no video appears?  
> **A:** Check:
   1. Grabber is sending pings (check server logs)
   2. Correct `streamType` in viewer request ("webcam" or "screen")
   3. WebRTC peer connection established (check browser console)
   4. Firewall allows UDP traffic for WebRTC

> **Q:** High CPU usage on SFU server?  
> **A:** Normal for many concurrent streams. If excessive:
   1. Check for memory leaks (monitor with `pprof`)
   2. Verify broadcaster goroutines are cleaned up
   3. Consider reducing `webcamTrackCount` or video resolution

> **Q:** Streams lag or freeze intermittently?  
> **A:** Usually network issues:
   1. Check packet loss between grabber and SFU
   2. Verify sufficient bandwidth
   3. Check if firewall is dropping UDP packets
   4. Enable TURN as fallback if ICE connection fails

> **Q:** How to check if the relay server is running?  
> **A:** Access the admin dashboard at `http://<server-ip>:<serverPort>` (default: `http://localhost:8000`). You should see the authentication page if `adminCredential` is set, or the dashboard directly if not.

> **Q:** Grabbers not appearing in admin dashboard?  
> **A:** Verify:
   1. Grabber successfully connected (check grabber logs)
   2. Grabber is sending pings (should see in server logs)
   3. Grabber name matches expected participant names in config
   4. No firewall blocking WebSocket connections

  

## License

---

This project is licensed under the [MIT license](https://en.wikipedia.org/wiki/MIT_License). You can freely use these tools in your commercial or open-source software.