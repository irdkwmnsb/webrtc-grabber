package signalling

import (
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

// Storage manages the registry of active grabbers (peers) and their metadata.
// It provides thread-safe operations for tracking peer connections, monitoring
// their health via ping timestamps, and managing the list of expected participants.
//
// Storage serves two main purposes:
//  1. Health monitoring: Tracks which grabbers are currently connected and responsive
//  2. Admin interface: Provides peer status information for the admin dashboard
//
// Peers are identified by their socket ID (typically remote IP:port) and have
// a human-readable name for easier identification in the UI.
//
// Thread safety: All public methods use mutex protection and are safe for
// concurrent access from multiple goroutines.
type Storage struct {
	// peers maps socket IDs to peer information including name, last ping time,
	// connection count, and available stream types
	peers map[sockets.SocketID]api.Peer

	// participants is the list of expected grabber names from configuration.
	// Used to show online/offline status in the admin interface
	participants []string

	// mutex protects concurrent access to peers and participants maps
	mutex sync.Mutex
}

// NewStorage creates a new Storage instance with initialized empty collections.
func NewStorage() *Storage {
	return &Storage{
		peers:        make(map[sockets.SocketID]api.Peer),
		participants: make([]string, 0),
	}
}

// addPeer registers a new peer (grabber) connection in the storage.
// This is called when a grabber connects to the /ws/peers/:name endpoint.
//
// The peer is created with:
//   - Name: The human-readable name from the URL path
//   - SocketId: The network address (IP:port) of the connection
//   - LastPing: Initially nil, updated when first ping arrives
//   - ConnectionsCount: Initially 0, updated via ping messages
//   - StreamTypes: Initially empty, updated via ping messages
//
// This method is thread-safe and returns the Storage instance for method chaining.
func (s *Storage) addPeer(name string, socketId sockets.SocketID) *Storage {
	newPeer := api.Peer{Name: name, SocketId: socketId}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.peers[newPeer.SocketId] = newPeer
	return s
}

// getPeerByName searches for a peer by its human-readable name.
// If multiple peers have the same name (e.g., reconnections with different IPs),
// returns the one with the most recent ping timestamp.
//
// This method is useful when subscribers specify a peer by name rather than
// socket ID, allowing connections to persist across grabber reconnections.
//
// Returns:
//   - peer: The found peer with the most recent ping
//   - ok: true if a peer with the given name was found, false otherwise
//
// This method is thread-safe.
func (s *Storage) getPeerByName(name string) (api.Peer, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	var peer api.Peer
	isFind := false
	for _, p := range s.peers {
		if p.Name != name {
			continue
		}
		if !isFind || peer.LastPing == nil || p.LastPing != nil && peer.LastPing.Before(*p.LastPing) {
			peer = p
			isFind = true
		}
	}
	return peer, isFind
}

// deletePeer removes a peer from the storage by its socket ID.
// This is called during manual cleanup or when a peer is detected as stale.
//
// This method is thread-safe.
func (s *Storage) deletePeer(streamId sockets.SocketID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.peers, streamId)
}

// deleteOldPeers removes peers that haven't sent a ping in over 60 seconds.
// This method is called periodically (every minute) via an interval timer
// to clean up stale peer entries from disconnected or crashed grabbers.
//
// A peer is considered stale if:
//   - LastPing is not nil (has pinged at least once)
//   - More than 60 seconds have elapsed since LastPing
//
// Peers that have never pinged (LastPing == nil) are not removed, giving
// newly connected grabbers time to send their first ping message.
//
// This method iterates over all peers and should complete quickly even
// with hundreds of peers due to the simple time comparison.
func (s *Storage) deleteOldPeers() {
	for peerSocketId, peer := range s.peers {
		if peer.LastPing != nil && time.Since(*peer.LastPing).Seconds() > 60 {
			s.deletePeer(peerSocketId)
		}
	}
}

// ping updates a peer's health status based on a received ping message.
// This method is called when a grabber sends a periodic ping message
// containing its current connection status.
//
// Updates:
//   - LastPing: Current timestamp, indicating the peer is alive
//   - ConnectionsCount: Number of active subscriber connections reported by grabber
//   - StreamTypes: List of stream types the grabber is currently providing
//
// The ConnectionsCount and StreamTypes come from the grabber's own tracking
// and may differ from the server's view due to timing or network issues.
//
// Parameters:
//   - socketId: The socket ID of the peer sending the ping
//   - status: The peer status information from the ping message
//
// This method is thread-safe.
func (s *Storage) ping(socketId sockets.SocketID, status api.PeerStatus) {
	now := time.Now()
	s.mutex.Lock()
	defer s.mutex.Unlock()

	peer := s.peers[socketId]
	peer.LastPing = &now
	peer.ConnectionsCount = status.ConnectionsCount
	peer.StreamTypes = status.StreamTypes
	peer.CurrentRecordId = status.CurrentRecordId
	s.peers[socketId] = peer
}

// getAll returns a snapshot of all currently registered peers.
// This is used by the admin interface to display the status of all
// connected grabbers.
//
// The returned slice is a copy, safe to iterate without holding locks.
// The order of peers in the slice is undefined.
//
// This method is thread-safe.
func (s *Storage) getAll() []api.Peer {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var peers []api.Peer
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	return peers
}

// getParticipantsStatus returns the status of all expected participants from configuration.
// This method creates a list matching the configured participants, showing which
// are online (with their current status) and which are offline (empty peer info).
//
// For each participant in the configuration:
//   - If online: Returns the full peer information including last ping, connections, etc.
//   - If offline: Returns a peer with only the name field populated
//
// This allows the admin interface to show a consistent list of expected grabbers
// regardless of their current connection status.
//
// The returned slice preserves the order of participants from the configuration.
//
// This method is thread-safe.
func (s *Storage) getParticipantsStatus() []api.Peer {
	var peers []api.Peer
	for _, participant := range s.participants {
		if peer, ok := s.getPeerByName(participant); ok {
			peers = append(peers, peer)
		} else {
			peers = append(peers, api.Peer{Name: participant})
		}
	}
	return peers
}

// setParticipants sets the list of expected participant names from configuration.
// This is called once during server initialization with the participants
// defined in conf/config.json.
//
// The participant list is used by getParticipantsStatus to provide a
// consistent view of expected grabbers in the admin interface.
//
// This method is not thread-safe and should only be called during initialization
// before the server starts handling requests.
func (s *Storage) setParticipants(participants []string) {
	s.participants = participants
}
