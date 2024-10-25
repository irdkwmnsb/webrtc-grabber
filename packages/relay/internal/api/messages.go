package api

import "github.com/pion/webrtc/v3"

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
)

const (
	GrabberMessageEventPing              = GrabberMessageEvent("ping")
	GrabberMessageEventInitPeer          = GrabberMessageEvent("init_peer")
	GrabberMessageEventOffer             = GrabberMessageEvent("offer")
	GrabberMessageEventOfferAnswer       = GrabberMessageEvent("offer_answer")
	GrabberMessageEventGrabberIce        = GrabberMessageEvent("grabber_ice")
	GrabberMessageEventPlayerIce         = GrabberMessageEvent("player_ice")
	GrabberMessageEventRecordStart       = GrabberMessageEvent("record_start")
	GrabberMessageEventRecordStop        = GrabberMessageEvent("record_stop")
	GrabberMessageEventRecordUpload      = GrabberMessageEvent("record_upload")
	GrabberMessageEventPlayersDisconnect = GrabberMessageEvent("players_disconnect")
)

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
}

type PlayerAuthMessage struct {
	Credential string `json:"credential"`
}

type GrabberMessage struct {
	Event        GrabberMessageEvent     `json:"event"`
	Ping         *PeerStatus             `json:"ping"`
	InitPeer     *GrabberInitPeerMessage `json:"initPeer"`
	Offer        *OfferMessage           `json:"offer"`
	OfferAnswer  *OfferAnswerMessage     `json:"offerAnswer"`
	Ice          *IceMessage             `json:"ice"`
	RecordStart  *RecordStartMessage     `json:"recordStart"`
	RecordStop   *RecordStopMessage      `json:"recordStop"`
	RecordUpload *RecordUploadMessage    `json:"recordUpload"`
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

type RecordStartMessage struct {
	RecordId    string `json:"recordId"`
	TimeoutMsec uint   `json:"timeout"`
}

type RecordStopMessage struct {
	RecordId string `json:"recordId"`
}

type RecordUploadMessage struct {
	RecordId string `json:"recordId"`
}
