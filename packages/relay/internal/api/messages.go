package api

import "github.com/pion/webrtc/v4"

type PlayerMessageEvent string
type GrabberMessageEvent string

const (
	PlayerMessageEventAuth        = PlayerMessageEvent("auth")
	PlayerMessageEventAuthRequest = PlayerMessageEvent("auth:request")
	PlayerMessageEventAuthFailed  = PlayerMessageEvent("auth:failed")
	PlayerMessageEventInitPeer    = PlayerMessageEvent("init_peer")
	PlayerMessageEventPeerStatus  = PlayerMessageEvent("peers")
	PlayerMessageEventOffer       = PlayerMessageEvent("offer")
	PlayerMessageEventOfferFailed = PlayerMessageEvent("offer:failed")
	PlayerMessageEventOfferAnswer = PlayerMessageEvent("offer_answer")
	PlayerMessageEventGrabberIce  = PlayerMessageEvent("grabber_ice")
	PlayerMessageEventPlayerIce   = PlayerMessageEvent("player_ice")
	PlayerMessageEventPing        = PlayerMessageEvent("ping")
	PlayerMessageEventPong        = PlayerMessageEvent("pong")
)

const (
	GrabberMessageEventPing        = GrabberMessageEvent("ping")
	GrabberMessageEventInitPeer    = GrabberMessageEvent("init_peer")
	GrabberMessageEventOffer       = GrabberMessageEvent("offer")
	GrabberMessageEventOfferAnswer = GrabberMessageEvent("offer_answer")
	GrabberMessageEventGrabberIce  = GrabberMessageEvent("grabber_ice")
	GrabberMessageEventPlayerIce   = GrabberMessageEvent("player_ice")
)

type PingMessage struct {
	Timestamp int64 `json:"timestamp"`
}

type PlayerMessage struct {
	Event              PlayerMessageEvent  `json:"event"`
	PlayerAuth         *PlayerAuthMessage  `json:"playerAuth"`
	PeersStatus        []Peer              `json:"peersStatus"`
	ParticipantsStatus []Peer              `json:"participantsStatus"`
	Offer              *OfferMessage       `json:"offer"`
	OfferAnswer        *OfferAnswerMessage `json:"offerAnswer"`
	Ice                *IceMessage         `json:"ice"`
	InitPeer           *PcConfigMessage    `json:"initPeer"`
	AccessMessage      *string             `json:"accessMessage"`
	Ping               *PingMessage        `json:"ping"`
}

type PlayerAuthMessage struct {
	Credential string `json:"credential"`
}

type GrabberMessage struct {
	Event       GrabberMessageEvent     `json:"event"`
	Ping        *PeerStatus             `json:"ping"`
	InitPeer    *GrabberInitPeerMessage `json:"initPeer"`
	Offer       *OfferMessage           `json:"offer"`
	OfferAnswer *OfferAnswerMessage     `json:"offerAnswer"`
	Ice         *IceMessage             `json:"ice"`
	//PlayerAuth         *PlayerAuthMessage `json:"playerAuth"`
	//PeersStatus        []Peer             `json:"peersStatus"`
	//ParticipantsStatus []Peer             `json:"participantsStatus"`
	//Offer              OfferMessage `json:"offer"`
}

type GrabberInitPeerMessage struct {
	PcConfigMessage
	PingInterval int `json:"pingInterval"`
}

type PcConfigMessage struct {
	PcConfig PeerConnectionConfig `json:"pcConfig"`
}

type OfferMessage struct {
	PeerId     *string                   `json:"peerId"`
	PeerName   *string                   `json:"peerName"`
	Offer      webrtc.SessionDescription `json:"offer"`
	StreamType string                    `json:"streamType"`
}

type OfferAnswerMessage struct {
	PeerId string                    `json:"peerId"`
	Answer webrtc.SessionDescription `json:"answer"`
}

type IceMessage struct {
	PeerId    *string                 `json:"peerId"`
	PeerName  *string                 `json:"peerName"`
	Candidate webrtc.ICECandidateInit `json:"candidate"`
}
