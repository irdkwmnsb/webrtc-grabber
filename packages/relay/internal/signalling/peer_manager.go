package signalling

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v3"
)

const (
	PublisherWatingTime = 20 * time.Second
	BufferSize          = 1500
	MaxConcurrentReads  = 10
)

type PeerManager struct {
	peerConnections          *SyncMapWrapper[string, *webrtc.PeerConnection]
	trackBroadcasters        *SyncMapWrapper[string, *TrackBroadcaster]
	subscribers              *SyncMapWrapper[string, []*webrtc.PeerConnection]
	setupInProgress          *SyncMapWrapper[string, chan struct{}]
	subscriberToPublisherKey *SyncMapWrapper[*webrtc.PeerConnection, string]
	subscriberMutexes        *SyncMapWrapper[string, *sync.Mutex]
	config                   ServerConfig

	api *webrtc.API

	ctx    context.Context
	cancel context.CancelFunc
}

func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

func NewPeerManager(config ServerConfig) (*PeerManager, error) {
	debug.SetGCPercent(20) // SPECIFIC THING

	mediaEngine := &webrtc.MediaEngine{}
	for _, codec := range config.Codecs {
		if err := mediaEngine.RegisterCodec(codec.Params, codec.Type); err != nil {
			return nil, fmt.Errorf("failed to register codec: %w", err)
		}
	}

	interceptorRegistry := &interceptor.Registry{}

	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return nil, fmt.Errorf("failed to register default interceptors: %w", err)
	}

	internalPliFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		return nil, fmt.Errorf("failed to create PLI factory: %w", err)
	}

	interceptorRegistry.Add(internalPliFactory)

	webrtcApi := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	ctx, cancel := context.WithCancel(context.Background())

	pm := &PeerManager{
		api:                      webrtcApi,
		config:                   config,
		ctx:                      ctx,
		cancel:                   cancel,
		peerConnections:          NewSyncMapWrapper[string, *webrtc.PeerConnection](),
		trackBroadcasters:        NewSyncMapWrapper[string, *TrackBroadcaster](),
		subscribers:              NewSyncMapWrapper[string, []*webrtc.PeerConnection](),
		setupInProgress:          NewSyncMapWrapper[string, chan struct{}](),
		subscriberToPublisherKey: NewSyncMapWrapper[*webrtc.PeerConnection, string](),
		subscriberMutexes:        NewSyncMapWrapper[string, *sync.Mutex](),
	}

	return pm, nil
}

func (pm *PeerManager) Close() {
	pm.cancel()

	pm.peerConnections.Range(func(key string, value *webrtc.PeerConnection) bool {
		if value != nil {
			_ = value.Close()
		}
		pm.peerConnections.Delete(key)
		return true
	})
}

func (pm *PeerManager) DeleteSubscriber(id sockets.SocketID) {
	pc, ok := pm.peerConnections.LoadAndDelete(string(id))
	if !ok || pc == nil {
		return
	}

	_ = pc.Close()

	publisherKey, ok := pm.subscriberToPublisherKey.LoadAndDelete(pc)
	if !ok {
		return
	}

	pm.trackBroadcasters.Range(func(key string, value *TrackBroadcaster) bool {
		if strings.HasPrefix(key, publisherKey+"_track_") {
			value.RemoveSubscriber(pc)
		}
		return true
	})
	log.Printf("REMOVE SUB FROM PUBLISHER")
	pm.removeSubscriberFromPublisher(publisherKey, pc)

	_, ok = pm.subscribers.Load(publisherKey)
	if !ok {
		pm.cleanupPublisher(publisherKey)
	}
}

func (pm *PeerManager) cleanupPublisher(publisherKey string) {
	pc, ok := pm.peerConnections.LoadAndDelete(publisherKey)
	if ok {
		if pc != nil {
			_ = pc.Close()
		}
	}

	pm.trackBroadcasters.Range(func(key string, value *TrackBroadcaster) bool {
		if strings.HasPrefix(key, publisherKey+"_track_") {
			value.Stop()
			pm.trackBroadcasters.Delete(key)
		}
		return true
	})

	subscribers, ok := pm.subscribers.LoadAndDelete(publisherKey)
	if ok {
		for _, sub := range subscribers {
			_ = sub.Close()
		}
	}
}

func (pm *PeerManager) removeSubscriberFromPublisher(publisherKey string, subscriber *webrtc.PeerConnection) {
	mutex, _ := pm.subscriberMutexes.LoadOrStore(publisherKey, &sync.Mutex{})

	mutex.Lock()
	defer mutex.Unlock()

	currentSubscribers, ok := pm.subscribers.Load(publisherKey)
	if !ok {
		return
	}

	var newSubscribers []*webrtc.PeerConnection

	for _, sub := range currentSubscribers {
		if sub != subscriber {
			newSubscribers = append(newSubscribers, sub)
		}
	}

	log.Printf("SUBS COUNT: %v", len(newSubscribers))

	if len(newSubscribers) == 0 {
		pm.cleanupPublisher(publisherKey)
		pm.subscribers.Delete(publisherKey)
		pm.subscriberMutexes.Delete(publisherKey)
	} else {
		pm.subscribers.Store(publisherKey, newSubscribers)
	}
}

func (pm *PeerManager) AddSubscriber(id, publisherSocketID sockets.SocketID,
	streamType string, c sockets.Socket,
	offer *webrtc.SessionDescription, publisherConn sockets.Socket) *api.PlayerMessage {

	publisherKey := getPublisherKey(publisherSocketID, streamType)
	setupDone := pm.ensureGrabberConnection(publisherKey, publisherSocketID, streamType, publisherConn)
	if !setupDone {
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	var trackCount int
	pm.trackBroadcasters.Range(func(key string, value *TrackBroadcaster) bool {
		if strings.HasPrefix(key, publisherKey+"_track_") {
			trackCount++
		}
		return true
	})

	if trackCount == 0 {
		log.Printf("no tracks available for %s/%s", publisherKey, streamType)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPC, err := pm.api.NewPeerConnection(pm.config.PeerConnectionConfig.WebrtcConfiguration())
	if err != nil {
		log.Printf("failed to create subscriber peer connection %s: %v", id, err)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPeerID := fmt.Sprintf("%s_%s", id, streamType)

	subscriberPC.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for subscriber %s stream %s: %s", id, streamType, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Player %s (%s) disconnected or failed, cleaning up", id, subscriberPeerID)
			pm.cleanupPlayerConnection(id, subscriberPC)
		}
	})

	pm.peerConnections.Store(string(id), subscriberPC)
	pm.addSubscriberToPublisher(publisherKey, subscriberPC)
	pm.subscriberToPublisherKey.Store(subscriberPC, publisherKey)

	log.Printf("Adding %d tracks to subscriber %s (%s)", trackCount, id, subscriberPeerID)

	tracksAdded := 0
	pm.trackBroadcasters.Range(func(key string, value *TrackBroadcaster) bool {
		if strings.HasPrefix(key, publisherKey+"_track_") {
			if _, err := subscriberPC.AddTrack(value.GetLocalTrack()); err != nil {
				log.Printf("failed to add track to subscriber %s: %v", id, err)
				return false
			}

			value.AddSubscriber(subscriberPC)
			tracksAdded++
		}
		return true
	})

	if tracksAdded == 0 {
		pm.cleanupPlayerConnection(id, subscriberPC)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	if err := subscriberPC.SetRemoteDescription(*offer); err != nil {
		log.Printf("failed to set remote description for subscriber %s (%s): %v", id, subscriberPeerID, err)
		_ = subscriberPC.Close()
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	subscriberPC.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			_ = c.WriteJSON(api.PlayerMessage{
				Event: api.PlayerMessageEventGrabberIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			})
		}
	})

	answer, err := subscriberPC.CreateAnswer(nil)
	if err != nil {
		log.Printf("failed to create answer for subscriber %s (%s): %v", id, subscriberPeerID, err)
		pm.cleanupPlayerConnection(id, subscriberPC)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	if err := subscriberPC.SetLocalDescription(answer); err != nil {
		log.Printf("failed to set local description for subscriber %s (%s): %v", id, subscriberPeerID, err)
		pm.cleanupPlayerConnection(id, subscriberPC)
		return &api.PlayerMessage{Event: api.PlayerMessageEventOfferFailed}
	}

	return &api.PlayerMessage{
		Event: api.PlayerMessageEventOfferAnswer,
		OfferAnswer: &api.OfferAnswerMessage{
			PeerId: publisherKey,
			Answer: answer,
		},
	}
}

func (pm *PeerManager) addSubscriberToPublisher(publisherKey string, subscriberPC *webrtc.PeerConnection) {
	mutex, _ := pm.subscriberMutexes.LoadOrStore(publisherKey, &sync.Mutex{})

	mutex.Lock()
	defer mutex.Unlock()

	currentSubscribers, ok := pm.subscribers.Load(publisherKey)
	if !ok {
		currentSubscribers = nil
	}

	newSubscribers := append(currentSubscribers, subscriberPC)
	pm.subscribers.Store(publisherKey, newSubscribers)
}

func (pm *PeerManager) ensureGrabberConnection(publisherKey string, publisherSocketID sockets.SocketID,
	streamType string, publisherConn sockets.Socket) bool {

	if _, ok := pm.peerConnections.Load(publisherKey); ok {
		return true
	}

	setupChan, setupInProgress := pm.setupInProgress.Load(publisherKey)
	if setupInProgress {
		<-setupChan

		_, ok := pm.peerConnections.Load(publisherKey)
		return ok
	}

	pm.subscribers.Store(publisherKey, []*webrtc.PeerConnection{})

	setupChanNew := make(chan struct{})
	pm.setupInProgress.Store(publisherKey, setupChanNew)

	go pm.setupGrabberPeerConnection(publisherSocketID, setupChanNew, streamType, publisherConn)

	<-setupChanNew

	_, ok := pm.peerConnections.Load(publisherKey)

	return ok
}

func (pm *PeerManager) cleanupPlayerConnection(id sockets.SocketID, subscriberPC *webrtc.PeerConnection) {
	pm.peerConnections.Delete(string(id))

	publisherKey, ok := pm.subscriberToPublisherKey.LoadAndDelete(subscriberPC)
	if ok {
		pm.trackBroadcasters.Range(func(key string, value *TrackBroadcaster) bool {
			if strings.HasPrefix(key, publisherKey+"_track_") {
				value.RemoveSubscriber(subscriberPC)
			}
			return true
		})

		pm.removeSubscriberFromPublisher(publisherKey, subscriberPC)

		subscribers, exists := pm.subscribers.Load(publisherKey)
		if exists {
			if len(subscribers) == 0 {
				pm.cleanupPublisher(publisherKey)
			}
		}
	}

	_ = subscriberPC.Close()
}

func (pm *PeerManager) SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit) {
	pc, ok := pm.peerConnections.Load(string(id))
	if !ok {
		log.Printf("no subscriber peer connections for %v", id)
		return
	}

	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to subscriber %v peer connection: %v", id, err)
	}
}

func (pm *PeerManager) DeletePublisher(id sockets.SocketID) {
	var keysToDelete []string
	pm.peerConnections.Range(func(key string, value *webrtc.PeerConnection) bool {
		if strings.HasPrefix(key, string(id)+"_") {
			keysToDelete = append(keysToDelete, key)
		}
		return true
	})

	for _, publisherKey := range keysToDelete {
		pm.cleanupPublisher(publisherKey)
	}
}

func (pm *PeerManager) OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription) {
	pc, ok := pm.peerConnections.Load(publisherKey)
	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if err := pc.SetRemoteDescription(answer); err != nil {
		log.Printf("failed to set remote description for publisher %s: %v", publisherKey, err)
	}
}

func (pm *PeerManager) AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit) {
	pc, ok := pm.peerConnections.Load(publisherKey)
	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to publisher %s: %v", publisherKey, err)
	}
}

func getTrackKey(publisherKey, trackID string) string {
	return fmt.Sprintf("%s_track_%s", publisherKey, trackID)
}

func (pm *PeerManager) setupGrabberPeerConnection(publisherSocketID sockets.SocketID, setupChan chan struct{},
	streamType string, publisherConn sockets.Socket) {
	publisherKey := getPublisherKey(publisherSocketID, streamType)
	log.Printf("Setting up publisher peer connection for %s, streamType=%s", publisherSocketID, streamType)

	channelClosed := false
	defer func() {
		pm.setupInProgress.Delete(publisherKey)
		if !channelClosed {
			close(setupChan)
			channelClosed = true
		}
	}()

	config := pm.config.PeerConnectionConfig.WebrtcConfiguration()
	pc, err := pm.api.NewPeerConnection(config)
	if err != nil {
		log.Printf("failed to create publisher peer connection %s: %v", publisherSocketID, err)
		return
	}

	pm.peerConnections.Store(publisherKey, pc)

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add video transceiver for publisher %s: %v", publisherSocketID, err)
		pm.peerConnections.Delete(publisherKey)
		_ = pc.Close()
		return
	}

	expectedTracks := 1
	if streamType == "webcam" {
		expectedTracks = pm.config.WebcamTrackCount

		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if err != nil {
			log.Printf("failed to add audio transceiver for publisher %s: %v", publisherSocketID, err)
			pm.peerConnections.Delete(publisherKey)
			_ = pc.Close()
			return
		}
	}

	var tracksReceived int32
	var tracksMu sync.Mutex
	var broadcasters []*TrackBroadcaster

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Track received: ID=%s, Kind=%s, Codec=%s, PayloadType=%d",
			remoteTrack.ID(), remoteTrack.Kind(), remoteTrack.Codec().MimeType, remoteTrack.Codec().PayloadType)

		broadcaster, err := NewTrackBroadcaster(remoteTrack, publisherSocketID)
		if err != nil {
			log.Printf("Failed to create broadcaster for publisher %s: %v", publisherSocketID, err)
			return
		}

		tracksMu.Lock()
		broadcasters = append(broadcasters, broadcaster)
		currentCount := len(broadcasters)
		tracksMu.Unlock()

		atomic.AddInt32(&tracksReceived, 1)
		log.Printf("Tracks received for %s: %d/%d", publisherSocketID, currentCount, expectedTracks)

		if currentCount >= expectedTracks && !channelClosed {
			for i, b := range broadcasters {
				trackKey := getTrackKey(publisherKey, fmt.Sprintf("track_%d", i))
				pm.trackBroadcasters.Store(trackKey, b)
			}
			close(setupChan)
			channelClosed = true
		}
	})

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			if err := publisherConn.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventPlayerIce,
				Ice: &api.IceMessage{
					PeerId:    &publisherKey,
					Candidate: candidate.ToJSON(),
				},
			}); err != nil {
				log.Printf("failed to send ICE candidate to publisher %s: %v", publisherSocketID, err)
			}
		}
	})

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state for publisher %s: %s", publisherSocketID, state.String())
		if state == webrtc.ICEConnectionStateDisconnected || state == webrtc.ICEConnectionStateFailed {
			log.Printf("Grabber %s disconnected or failed, cleaning up", publisherKey)
			pm.cleanupPublisher(publisherKey)
			if !channelClosed {
				close(setupChan)
				channelClosed = true
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("failed to create offer for publisher %s: %v", publisherSocketID, err)
		pm.peerConnections.Delete(publisherKey)
		_ = pc.Close()
		return
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("failed to set local description for publisher %s: %v", publisherSocketID, err)
		pm.peerConnections.Delete(publisherKey)
		_ = pc.Close()
		return
	}

	if err := publisherConn.WriteJSON(api.GrabberMessage{
		Event: api.GrabberMessageEventOffer,
		Offer: &api.OfferMessage{
			Offer:      offer,
			PeerId:     &publisherKey,
			StreamType: streamType,
		},
	}); err != nil {
		log.Printf("failed to send offer to publisher %s: %v", publisherSocketID, err)
		pm.peerConnections.Delete(publisherKey)
		_ = pc.Close()
		return
	}

	timer := time.NewTimer(PublisherWatingTime)
	defer timer.Stop()

	select {
	case <-setupChan:
		log.Printf("All expected tracks received for grabber %s", publisherSocketID)
	case <-timer.C:
		log.Printf("Timeout waiting for tracks from publisher %s", publisherSocketID)
		pm.cleanupPublisher(publisherKey)
	}
}
