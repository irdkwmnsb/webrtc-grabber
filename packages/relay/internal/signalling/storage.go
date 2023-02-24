package signalling

import (
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"sync"
	"time"
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
	return s
}

func (s *Storage) getPeerByName(name string) *api.Peer {
	var peer *api.Peer
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, v := range s.peers {
		if v.Name != name {
			continue
		}
		if peer == nil || v.LastPing != nil && peer.LastPing.Before(*v.LastPing) {
			peer = &v
		}
	}
	return peer
}

func (s *Storage) deletePeer(streamId sockets.SocketID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.peers, streamId)
}

func (s *Storage) deleteOldPeers() {
	for peerSocketId, peer := range s.peers {
		if peer.LastPing != nil && time.Until(*peer.LastPing).Seconds() > 60 {
			s.deletePeer(peerSocketId)
		}
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
		peer := s.getPeerByName(participant)
		if peer != nil {
			peers = append(peers, *peer)
		} else {
			peers = append(peers, api.Peer{Name: participant})
		}
	}
	return peers
}

func (s *Storage) setParticipants(participants []string) {
	s.participants = participants
}
