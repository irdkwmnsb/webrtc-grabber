package signalling

import (
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/metrics"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type Storage struct {
	peers        map[sockets.SocketID]api.Peer
	participants []config.ParticipantInfo
	mutex        sync.RWMutex
}

func NewStorage() *Storage {
	return &Storage{
		peers:        make(map[sockets.SocketID]api.Peer),
		participants: make([]config.ParticipantInfo, 0),
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

func (s *Storage) removePeer(socketId sockets.SocketID) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if _, ok := s.peers[socketId]; ok {
		delete(s.peers, socketId)
		metrics.StoredPeers.Set(float64(len(s.peers)))
	}
}

func (s *Storage) getPeerByNameLocked(name string) (api.Peer, bool) {
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

func (s *Storage) getPeerByName(name string) (api.Peer, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.getPeerByNameLocked(name)
}

func (s *Storage) deleteOldPeers() {
	// So because we do not have thousands peers in production, this code is ok
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for peerSocketId, peer := range s.peers {
		if peer.LastPing != nil && time.Since(*peer.LastPing).Seconds() > 60 {
			delete(s.peers, peerSocketId)
			metrics.StoredPeers.Set(float64(len(s.peers)))
		}
	}
}

func (s *Storage) ping(socketId sockets.SocketID, status *api.PeerStatus) {
	now := time.Now()
	s.mutex.Lock()
	defer s.mutex.Unlock()

	peer := s.peers[socketId]
	peer.LastPing = &now
	peer.ConnectionsCount = status.ConnectionsCount
	peer.StreamTypes = status.StreamTypes
	peer.CurrentRecordId = status.CurrentRecordId
	peer.ProctoringActiveStreams = status.ProctoringActiveStreams
	peer.ProctoringHealth = status.ProctoringHealth
	peer.SiteChecks = status.SiteChecks
	s.peers[socketId] = peer
}

func (s *Storage) getAll() []api.Peer {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	var peers []api.Peer
	for _, peer := range s.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (s *Storage) getParticipantsStatus() []api.Peer {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	listed := make(map[string]struct{}, len(s.participants))
	result := make([]api.Peer, 0, len(s.participants))

	for _, participant := range s.participants {
		listed[participant.Name] = struct{}{}
		if peer, ok := s.getPeerByNameLocked(participant.Name); ok {
			peer.TeamName = participant.TeamName
			peer.University = participant.University
			result = append(result, peer)
		} else {
			result = append(result, api.Peer{
				Name:       participant.Name,
				TeamName:   participant.TeamName,
				University: participant.University,
			})
		}
	}

	unlisted := make(map[string]api.Peer)
	for _, peer := range s.peers {
		if _, ok := listed[peer.Name]; ok {
			continue
		}
		if existing, ok := unlisted[peer.Name]; ok {
			if existing.LastPing != nil && (peer.LastPing == nil || existing.LastPing.After(*peer.LastPing)) {
				continue
			}
		}
		unlisted[peer.Name] = peer
	}
	for _, peer := range unlisted {
		result = append(result, peer)
	}
	return result
}

func (s *Storage) peerNamesBySocketId() map[sockets.SocketID]string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	m := make(map[sockets.SocketID]string, len(s.peers))
	for id, p := range s.peers {
		m[id] = p.Name
	}
	return m
}

func (s *Storage) setParticipants(participants []config.ParticipantInfo) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.participants = participants
}
