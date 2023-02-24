package api

type PlayerMessageEvent string
type GrabberMessageEvent string

const (
	PlayerMessageEventAuth        = PlayerMessageEvent("auth")
	PlayerMessageEventAuthRequest = PlayerMessageEvent("auth:request")
	PlayerMessageEventAuthFailed  = PlayerMessageEvent("auth:failed")
	PlayerMessageEventPeerStatus  = PlayerMessageEvent("peers")
	PlayerMessageEventOffer       = PlayerMessageEvent("offer")
	PlayerMessageEventOfferAnswer = PlayerMessageEvent("offer_answer")
	PlayerMessageEventGrabberIce  = PlayerMessageEvent("grabber_ice")
	PlayerMessageEventPlayerIce   = PlayerMessageEvent("player_ice")
)

const (
	GrabberMessageEventPing        = GrabberMessageEvent("ping")
	GrabberMessageEventInitPeer    = GrabberMessageEvent("init_peer")
	GrabberMessageEventOffer       = GrabberMessageEvent("offer")
	GrabberMessageEventOfferAnswer = GrabberMessageEvent("offer_answer")
	GrabberMessageEventGrabberIce  = GrabberMessageEvent("grabber_ice")
	GrabberMessageEventPlayerIce   = GrabberMessageEvent("player_ice")
)

type PlayerMessage struct {
	Event              PlayerMessageEvent  `json:"event"`
	PlayerAuth         *PlayerAuthMessage  `json:"playerAuth"`
	PeersStatus        []Peer              `json:"peersStatus"`
	ParticipantsStatus []Peer              `json:"participantsStatus"`
	Offer              *OfferMessage       `json:"offer"`
	OfferAnswer        *OfferAnswerMessage `json:"offerAnswer"`
	Ice                *IceMessage         `json:"ice"`
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
	PcConfig     any `json:"pcConfig"`
	PingInterval int `json:"pingInterval"`
}

type OfferMessage struct {
	PeerId     string `json:"peerId"`
	Offer      any    `json:"offer"`
	StreamType string `json:"streamType"`
}

type OfferAnswerMessage struct {
	PeerId string `json:"peerId"`
	Answer any    `json:"answer"`
}

type IceMessage struct {
	PeerId    string      `json:"peerId"`
	Candidate interface{} `json:"candidate"`
}
