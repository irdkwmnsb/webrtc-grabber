package signalling

import (
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type Storage struct {
	peers        map[sockets.SocketID]api.Peer
	participants []string
	mutex        sync.Mutex
}

func NewStorage() *Storage {
	return &Storage{
		peers:        make(map[sockets.SocketID]api.Peer),
		participants: make([]string, 0),
	}
}

func (s *Storage) addPeer(name string, socketId sockets.SocketID) *Storage {
	newPeer := api.Peer{Name: name, SocketId: socketId}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.peers[newPeer.SocketId] = newPeer
	metrics.StoredPeers.Set(float64(len(s.peers)))
	return s
}

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

func (s *Storage) deletePeer(streamId sockets.SocketID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.peers, streamId)
	metrics.StoredPeers.Set(float64(len(s.peers)))
}

func (s *Storage) deleteOldPeers() {
	peersToDelete := make([]sockets.SocketID, 0)
	for peerSocketId, peer := range s.peers {
		if peer.LastPing != nil && time.Since(*peer.LastPing).Seconds() > 60 {
			peersToDelete = append(peersToDelete, peerSocketId)
		}
	}

	for _, peerSocketId := range peersToDelete {
		s.deletePeer(peerSocketId)
	}
}

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

func (s *Storage) getAll() []api.Peer {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var peers []api.Peer
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	return peers
}

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

func (s *Storage) setParticipants(participants []string) {
	s.participants = participants
}
