package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ActiveWebSocketConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_websocket_connections",
		Help: "Number of active WebSocket connections",
	})

	WebSocketConnectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_websocket_connections_total",
		Help: "Total number of WebSocket connections",
	})

	WebSocketDisconnectionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_websocket_disconnections_total",
		Help: "Total number of WebSocket disconnections",
	})

	ActiveAgents = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_agents",
		Help: "Number of active agents (grabbers)",
	})

	AgentsRegisteredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_agents_registered_total",
		Help: "Total number of agents registered",
	})

	ActivePlayers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_players",
		Help: "Number of active players (viewers)",
	})

	PlayersConnectedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_players_connected_total",
		Help: "Total number of players connected",
	})

	ActivePeerConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_peer_connections",
		Help: "Number of active WebRTC peer connections",
	})

	PeerConnectionsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_peer_connections_created_total",
		Help: "Total number of WebRTC peer connections created",
	})

	PeerConnectionFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_peer_connection_failures_total",
		Help: "Total number of WebRTC peer connection failures",
	}, []string{"reason"})

	ActiveTracks = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_tracks",
		Help: "Number of active media tracks",
	}, []string{"type"})

	TracksAddedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_tracks_added_total",
		Help: "Total number of media tracks added",
	}, []string{"type"})

	SFUBytesReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_sfu_bytes_received_total",
		Help: "Total bytes received by SFU",
	})

	SFUBytesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_sfu_bytes_sent_total",
		Help: "Total bytes sent by SFU",
	})

	SFUPacketsReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_sfu_packets_received_total",
		Help: "Total packets received by SFU",
	})

	SFUPacketsSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_sfu_packets_sent_total",
		Help: "Total packets sent by SFU",
	})

	SFUPacketsLost = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_sfu_packets_lost_total",
		Help: "Total packets lost in SFU",
	})

	ICECandidatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_ice_candidates_total",
		Help: "Total number of ICE candidates",
	}, []string{"type"})

	GoRoutines = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_goroutines",
		Help: "Number of goroutines",
	})

	LastPings = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_last_pings",
		Help: "Unix-time of last pings from peer",
	}, []string{"peer"})

	LastAudioPacket = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_last_audio_packet",
		Help: "Unix-time of last audio packet",
	}, []string{"peer"})

	IsStreamActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_is_stream_active",
		Help: "Whether peer has subscribers on the stream type",
	}, []string{"peer", "type"})
)
