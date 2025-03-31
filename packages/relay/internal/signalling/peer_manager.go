package signalling

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/webrtc/v3"
)

const PublisherWatingTime = 20 * time.Second

type PeerManager struct {
	peerConnections          map[string]*webrtc.PeerConnection
	publisherTracks          map[string][]webrtc.TrackLocal
	subscribers              map[string][]*webrtc.PeerConnection
	setupInProgress          map[string]chan struct{}
	subscriberToPublisherKey map[*webrtc.PeerConnection]string
	config                   ServerConfig

	mu sync.RWMutex

	api *webrtc.API
}

func getPublisherKey(publisherSocketID sockets.SocketID, streamType string) string {
	return fmt.Sprintf("%s_%s", string(publisherSocketID), streamType)
}

func NewPeerManager(config ServerConfig) (*PeerManager, error) {
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

	return &PeerManager{
		peerConnections:          make(map[string]*webrtc.PeerConnection),
		publisherTracks:          make(map[string][]webrtc.TrackLocal),
		subscribers:              make(map[string][]*webrtc.PeerConnection),
		setupInProgress:          make(map[string]chan struct{}),
		subscriberToPublisherKey: make(map[*webrtc.PeerConnection]string),
		api:                      webrtcApi,
		config:                   config,
	}, nil
}

func (pm *PeerManager) Close() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for id, pc := range pm.peerConnections {
		if pc != nil {
			_ = pc.Close()
		}
		delete(pm.peerConnections, id)
	}
}

func (pm *PeerManager) DeleteSubscriber(id sockets.SocketID) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pc, ok := pm.peerConnections[string(id)]
	if !ok || pc == nil {
		return
	}

	_ = pc.Close()
	publisherKey, ok := pm.subscriberToPublisherKey[pc]
	if !ok {
		return
	}

	pm.removeSubscriberFromPublisher(publisherKey, pc)

	if len(pm.subscribers[publisherKey]) == 0 {
		pm.cleanupPublisher(publisherKey)
	}

	delete(pm.subscriberToPublisherKey, pc)
}

func (pm *PeerManager) cleanupPublisher(publisherKey string) {
	pc, ok := pm.peerConnections[publisherKey]
	if ok && pc != nil {
		_ = pc.Close()
		delete(pm.peerConnections, publisherKey)
		delete(pm.publisherTracks, publisherKey)
		delete(pm.subscribers, publisherKey)
	}
}

func (pm *PeerManager) removeSubscriberFromPublisher(publisherKey string, subscriber *webrtc.PeerConnection) {
	subscribers := pm.subscribers[publisherKey]
	for i, sub := range subscribers {
		if sub == subscriber {
			pm.subscribers[publisherKey] = append(subscribers[:i], subscribers[i+1:]...)
			break
		}
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

	pm.mu.RLock()
	tracks := pm.publisherTracks[publisherKey]
	trackCount := len(tracks)
	pm.mu.RUnlock()

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
	pm.mu.Lock()
	pm.peerConnections[string(id)] = subscriberPC
	pm.subscribers[publisherKey] = append(pm.subscribers[publisherKey], subscriberPC)
	pm.subscriberToPublisherKey[subscriberPC] = publisherKey

	log.Printf("Adding %d tracks to subscriber %s (%s)", len(tracks), id, subscriberPeerID)
	trackAddSuccess := true

	for _, track := range tracks {
		var sender *webrtc.RTPSender
		if sender, err = subscriberPC.AddTrack(track); err != nil {
			log.Printf("failed to add track to subscriber %s: %v", id, err)
			trackAddSuccess = false
			break
		}

		go func(sender *webrtc.RTPSender) {
			buf := make([]byte, 1500)
			for {
				if _, _, err := sender.Read(buf); err != nil {
					return
				}
			}
		}(sender)
	}
	pm.mu.Unlock()

	if !trackAddSuccess {
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

func (pm *PeerManager) ensureGrabberConnection(publisherKey string, publisherSocketID sockets.SocketID,
	streamType string, publisherConn sockets.Socket) bool {
	pm.mu.Lock()

	if _, ok := pm.peerConnections[publisherKey]; ok {
		pm.mu.Unlock()
		return true
	}

	setupChan, setupInProgress := pm.setupInProgress[publisherKey]
	if setupInProgress {
		pm.mu.Unlock()
		<-setupChan

		pm.mu.RLock()
		_, ok := pm.peerConnections[publisherKey]
		pm.mu.RUnlock()
		return ok
	}

	pm.publisherTracks[publisherKey] = []webrtc.TrackLocal{}
	pm.subscribers[publisherKey] = []*webrtc.PeerConnection{}

	setupChan = make(chan struct{})
	pm.setupInProgress[publisherKey] = setupChan
	pm.mu.Unlock()

	go pm.setupGrabberPeerConnection(publisherSocketID, setupChan, streamType, publisherConn)

	<-setupChan

	pm.mu.RLock()
	_, ok := pm.peerConnections[publisherKey]
	pm.mu.RUnlock()

	return ok
}

func (pm *PeerManager) cleanupPlayerConnection(id sockets.SocketID, subscriberPC *webrtc.PeerConnection) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.peerConnections, string(id))

	publisherKey, ok := pm.subscriberToPublisherKey[subscriberPC]
	if ok {
		subscribers := pm.subscribers[publisherKey]
		for i, sub := range subscribers {
			if sub == subscriberPC {
				pm.subscribers[publisherKey] = append(subscribers[:i], subscribers[i+1:]...)
				break
			}
		}

		if len(pm.subscribers[publisherKey]) == 0 {
			if publisherPC, ok := pm.peerConnections[publisherKey]; ok && publisherPC != nil {
				_ = publisherPC.Close()
				delete(pm.peerConnections, publisherKey)
				delete(pm.publisherTracks, publisherKey)
				delete(pm.subscribers, publisherKey)
			}
		}

		delete(pm.subscriberToPublisherKey, subscriberPC)
	}

	_ = subscriberPC.Close()
}

func (pm *PeerManager) SubscriberICE(id sockets.SocketID, candidate webrtc.ICECandidateInit) {
	pm.mu.RLock()
	pc, ok := pm.peerConnections[string(id)]
	pm.mu.RUnlock()

	if !ok {
		log.Printf("no subscriber peer connections for %v", id)
		return
	}

	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to subscriber %v peer connection: %v", id, err)
	}
}

func (pm *PeerManager) DeletePublisher(id sockets.SocketID) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var keysToDelete []string
	for publisherKey := range pm.peerConnections {
		if strings.HasPrefix(publisherKey, string(id)+"_") {
			keysToDelete = append(keysToDelete, publisherKey)
		}
	}

	for _, publisherKey := range keysToDelete {
		if pc := pm.peerConnections[publisherKey]; pc != nil {
			_ = pc.Close()
		}

		for _, subscriberPC := range pm.subscribers[publisherKey] {
			if subscriberPC != nil {
				_ = subscriberPC.Close()
			}
		}

		delete(pm.peerConnections, publisherKey)
		delete(pm.publisherTracks, publisherKey)
		delete(pm.subscribers, publisherKey)
	}
}

func (pm *PeerManager) OfferAnswerPublisher(publisherKey string, answer webrtc.SessionDescription) {
	pm.mu.RLock()
	pc, ok := pm.peerConnections[publisherKey]
	pm.mu.RUnlock()

	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if err := pc.SetRemoteDescription(answer); err != nil {
		log.Printf("failed to set remote description for publisher %s: %v", publisherKey, err)
	}
}

func (pm *PeerManager) AddICECandidatePublisher(publisherKey string, candidate webrtc.ICECandidateInit) {
	pm.mu.RLock()
	pc, ok := pm.peerConnections[publisherKey]
	pm.mu.RUnlock()

	if !ok {
		log.Printf("no peer connection for publisher %s", publisherKey)
		return
	}

	if err := pc.AddICECandidate(candidate); err != nil {
		log.Printf("failed to add ICE candidate to publisher %s: %v", publisherKey, err)
	}
}

func (pm *PeerManager) setupGrabberPeerConnection(publisherSocketID sockets.SocketID, setupChan chan struct{},
	streamType string, publisherConn sockets.Socket) {
	publisherKey := getPublisherKey(publisherSocketID, streamType)
	log.Printf("Setting up publisher peer connection for %s, streamType=%s", publisherSocketID, streamType)

	channelClosed := false
	defer func() {
		pm.mu.Lock()
		delete(pm.setupInProgress, publisherKey)
		pm.mu.Unlock()

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

	pm.mu.Lock()
	pm.peerConnections[publisherKey] = pc
	pm.mu.Unlock()

	_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		log.Printf("failed to add video transceiver for publisher %s: %v", publisherSocketID, err)
		pm.mu.Lock()
		delete(pm.peerConnections, publisherKey)
		pm.mu.Unlock()
		_ = pc.Close()
		return
	}

	expectedTracks := 1
	if streamType == "webcam" {
		expectedTracks = 2

		_, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		})
		if err != nil {
			log.Printf("failed to add audio transceiver for publisher %s: %v", publisherSocketID, err)
			pm.mu.Lock()
			delete(pm.peerConnections, publisherKey)
			pm.mu.Unlock()
			_ = pc.Close()
			return
		}
	}

	var tracksReceived int
	var tracksMu sync.Mutex

	pc.OnTrack(func(remoteTrack *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		log.Printf("Track received: ID=%s, Kind=%s, Codec=%s, PayloadType=%d",
			remoteTrack.ID(), remoteTrack.Kind(), remoteTrack.Codec().MimeType, remoteTrack.Codec().PayloadType)

		localTrack, err := webrtc.NewTrackLocalStaticRTP(remoteTrack.Codec().RTPCodecCapability,
			remoteTrack.ID(), remoteTrack.StreamID())
		if err != nil {
			log.Printf("failed to create TrackLocal for publisher %s: %v", publisherSocketID, err)
			return
		}

		pm.mu.Lock()
		pm.publisherTracks[publisherKey] = append(pm.publisherTracks[publisherKey], localTrack)
		pm.mu.Unlock()

		tracksMu.Lock()
		tracksReceived++
		log.Printf("Tracks received for %s: %d/%d", publisherSocketID, tracksReceived, expectedTracks)

		if tracksReceived >= expectedTracks && !channelClosed {
			close(setupChan)
			channelClosed = true
		}
		tracksMu.Unlock()

		go func() {
			buffer := make([]byte, 1500)

			for {
				n, _, err := remoteTrack.Read(buffer)
				if err != nil {
					log.Printf("error reading RTP from publisher %s: %v", publisherSocketID, err)
					return
				}

				if _, err := localTrack.Write(buffer[:n]); err != nil {
					log.Printf("error writing RTP to TrackLocal for publisher %s: %v", publisherSocketID, err)
				}
			}
		}()
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

			pm.mu.Lock()
			if _, exists := pm.peerConnections[publisherKey]; exists {
				_ = pc.Close()
				delete(pm.peerConnections, publisherKey)
				delete(pm.publisherTracks, publisherKey)

				for _, pcPlayer := range pm.subscribers[publisherKey] {
					_ = pcPlayer.Close()
				}
				delete(pm.subscribers, publisherKey)
			}
			pm.mu.Unlock()

			if !channelClosed {
				close(setupChan)
				channelClosed = true
			}
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		log.Printf("failed to create offer for publisher %s: %v", publisherSocketID, err)

		pm.mu.Lock()
		delete(pm.peerConnections, publisherKey)
		pm.mu.Unlock()

		_ = pc.Close()
		return
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		log.Printf("failed to set local description for publisher %s: %v", publisherSocketID, err)

		pm.mu.Lock()
		delete(pm.peerConnections, publisherKey)
		pm.mu.Unlock()

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

		pm.mu.Lock()
		delete(pm.peerConnections, publisherKey)
		pm.mu.Unlock()

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

		pm.mu.Lock()
		_ = pc.Close()
		delete(pm.peerConnections, publisherKey)
		delete(pm.publisherTracks, publisherKey)
		delete(pm.subscribers, publisherKey)
		pm.mu.Unlock()
	}
}
