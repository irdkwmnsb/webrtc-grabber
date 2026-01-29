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

	// ActivePeerConnections tracks active WebRTC peer connections by role
	ActivePeerConnections = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_peer_connections",
		Help: "Number of active WebRTC peer connections",
	}, []string{"role"}) // "publisher" | "subscriber"

	PeerConnectionsCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_peer_connections_created_total",
		Help: "Total number of WebRTC peer connections created",
	}, []string{"role"}) // "publisher" | "subscriber"

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

	ICECandidatesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_ice_candidates_total",
		Help: "Total number of ICE candidates",
	}, []string{"type"})

	ConnectionDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "webrtc_grabber_connection_duration_seconds",
		Help:    "Duration of WebRTC connections",
		Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s to ~1h
	}, []string{"role"})

	ICEConnectionState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "webrtc_grabber_ice_connection_state",
		Help: "Current ICE connection state (1=new, 2=checking, 3=connected, 4=completed, 5=failed, 6=disconnected, 7=closed)",
	}, []string{"peer_type"})

	NACKRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_nack_requests_total",
		Help: "Total NACK requests (indicates packet loss)",
	})

	PLIRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_pli_requests_total",
		Help: "Total PLI requests (indicates video quality issues)",
	})

	SignallingMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_signalling_messages_total",
		Help: "Total signalling messages",
	}, []string{"type", "direction"}) // direction: "in" | "out"

	WebSocketMessagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_websocket_messages_total",
		Help: "Total WebSocket messages",
	}, []string{"endpoint", "direction"}) // endpoint: "grabber" | "player_admin" | "player_play"

	PeerConnectionSetupDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "webrtc_grabber_peer_connection_setup_seconds",
		Help:    "Time to setup peer connection",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 100ms to ~50s
	}, []string{"role"})

	TracksPerPublisher = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "webrtc_grabber_tracks_per_publisher",
		Help:    "Number of tracks per publisher",
		Buckets: []float64{1, 2, 3, 4, 5, 10},
	})

	SubscribersPerPublisher = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "webrtc_grabber_subscribers_per_publisher",
		Help:    "Number of subscribers per publisher",
		Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100},
	})

	RTPPacketsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_rtp_packets_total",
		Help: "Total RTP packets processed",
	}, []string{"direction"}) // "received" | "forwarded"

	RTPBytesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_rtp_bytes_total",
		Help: "Total RTP bytes processed",
	}, []string{"direction"})

	ICEGatheringDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "webrtc_grabber_ice_gathering_seconds",
		Help:    "Time spent gathering ICE candidates",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10), // 10ms to ~5s
	}, []string{"role"})

	PeerConnectionStateChanges = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "webrtc_grabber_peer_connection_state_changes_total",
		Help: "Peer connection state changes",
	}, []string{"role", "state"})

	ActiveBroadcasters = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_active_broadcasters",
		Help: "Number of active track broadcasters",
	})

	StoredPeers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_stored_peers",
		Help: "Number of peers in storage",
	})

	ConfigReloads = promauto.NewCounter(prometheus.CounterOpts{
		Name: "webrtc_grabber_config_reloads_total",
		Help: "Number of configuration reloads",
	})

	StartTime = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "webrtc_grabber_start_time_seconds",
		Help: "Server start time in Unix seconds",
	})
)
