package api

import (
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/proctoring"
	"github.com/pion/webrtc/v4"
)

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
	PlayerMessageEventProctoring  = PlayerMessageEvent("proctoring")
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
	GrabberMessageEventProctoringStart   = GrabberMessageEvent("proctoring_start")
	GrabberMessageEventProctoringPause   = GrabberMessageEvent("proctoring_pause")
	GrabberMessageEventProctoringResume  = GrabberMessageEvent("proctoring_resume")
	GrabberMessageEventProctoringStop    = GrabberMessageEvent("proctoring_stop")
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
	Proctoring         *proctoring.State   `json:"proctoring,omitempty"`
}

type PlayerAuthMessage struct {
	Credential string `json:"credential"`
}

type ProctoringConfigMessage struct {
	SessionId       string `json:"sessionId"`
	ChunkDurationMs uint32 `json:"chunkDurationMs"`
	Fps             uint32 `json:"fps"`
	VideoBitrate    uint32 `json:"videoBitrate"`
	UploadToken     string `json:"uploadToken,omitempty"`
}

type GrabberMessage struct {
	Event            GrabberMessageEvent      `json:"event"`
	Ping             *PeerStatus              `json:"ping"`
	InitPeer         *GrabberInitPeerMessage  `json:"initPeer"`
	Offer            *OfferMessage            `json:"offer"`
	OfferAnswer      *OfferAnswerMessage      `json:"offerAnswer"`
	Ice              *IceMessage              `json:"ice"`
	RecordStart      *RecordStartMessage      `json:"recordStart"`
	RecordStop       *RecordStopMessage       `json:"recordStop"`
	RecordUpload     *RecordUploadMessage     `json:"recordUpload"`
	ProctoringStart  *ProctoringConfigMessage `json:"proctoringStart,omitempty"`
	ProctoringResume *ProctoringConfigMessage `json:"proctoringResume,omitempty"`
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
	UploadToken string `json:"uploadToken,omitempty"`
}

type RecordStopMessage struct {
	RecordId string `json:"recordId"`
}

type RecordUploadMessage struct {
	RecordId string `json:"recordId"`
}
