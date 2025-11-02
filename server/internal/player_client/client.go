package player_client

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/fasthttp/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/pkg/api"
	"github.com/irdkwmnsb/webrtc-grabber/internal/sockets"
	"github.com/pion/webrtc/v4"
)

type Config struct {
	SignallingUrl string
	Credential    string
}

type ConnectionCtx struct {
	context.Context
	config   ConnectionConfig
	ws       sockets.Socket
	PCConfig api.PeerConnectionConfig
}

func (ctx *ConnectionCtx) SendICECandidate(candidate *webrtc.ICECandidate) error {
	if candidate == nil {
		return nil
	}
	return ctx.ws.WriteJSON(api.PlayerMessage{
		Event: api.PlayerMessageEventPlayerIce,
		Ice: &api.IceMessage{
			PeerName:  &ctx.config.PeerName,
			Candidate: candidate.ToJSON(),
		},
	})
}

type OfferCallback func(ctx ConnectionCtx) (webrtc.SessionDescription, error)
type OfferAnswerCallback func(ctx ConnectionCtx, answer webrtc.SessionDescription) error
type GrabberIceCallback func(ctx ConnectionCtx, ice webrtc.ICECandidateInit) error
type ConnectionConfig struct {
	PeerName      string
	StreamType    string
	GetOffer      OfferCallback
	OnOfferAnswer OfferAnswerCallback
	OnGrabberIce  GrabberIceCallback
}

type Client interface {
	ConnectToPeer(ctx context.Context, cfg ConnectionConfig) error
}

type grabberPlayerClient struct {
	config Config
}

func (g *grabberPlayerClient) ConnectToPeer(ctx_ context.Context, cfg ConnectionConfig) error {
	ctx := ConnectionCtx{
		Context: ctx_,
		config:  cfg,
	}

	log.Printf("connecting to %s", g.getWebSocketClientUrl())

	_ws, _, err := websocket.DefaultDialer.DialContext(ctx, g.getWebSocketClientUrl(), nil)
	ws := sockets.NewFastHttpSocket(_ws)
	ctx.ws = ws
	if err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = ws.Close()
	}()

	if err := ws.WriteJSON(api.PlayerMessage{
		Event:      api.PlayerMessageEventAuth,
		PlayerAuth: &api.PlayerAuthMessage{Credential: g.config.Credential},
	}); err != nil {
		return err
	}
	var initPeerMessage api.PlayerMessage
	if err := ws.ReadJSON(&initPeerMessage); err != nil {
		return err
	} else if initPeerMessage.Event != api.PlayerMessageEventInitPeer || initPeerMessage.InitPeer == nil {
		return errors.New("grabber_client: no init peer message")
	}
	ctx.PCConfig = initPeerMessage.InitPeer.PcConfig

	webrtcOffer, err := cfg.GetOffer(ctx)
	if err != nil {
		return err
	}

	if err := ws.WriteJSON(api.PlayerMessage{
		Event: api.PlayerMessageEventOffer,
		Offer: &api.OfferMessage{Offer: webrtcOffer, PeerName: &cfg.PeerName, StreamType: cfg.StreamType},
	}); err != nil {
		return err
	}

	var message api.PlayerMessage
	for {
		if err = ws.ReadJSON(&message); err != nil {
			return err
		}

		switch {
		case message.Event == api.PlayerMessageEventOfferAnswer && message.OfferAnswer != nil:
			if err = cfg.OnOfferAnswer(ctx, message.OfferAnswer.Answer); err != nil {
				return err
			}
		case message.Event == api.PlayerMessageEventGrabberIce && message.Ice != nil:
			if err = cfg.OnGrabberIce(ctx, message.Ice.Candidate); err != nil {
				return err
			}
		}
	}
}

func (g *grabberPlayerClient) getWebSocketClientUrl() string {
	baseUrl := g.config.SignallingUrl
	if strings.HasPrefix(baseUrl, "http") {
		baseUrl = "ws" + baseUrl[4:]
	}
	return baseUrl + "/ws/player/play"
}

func NewClient(config Config) Client {
	return &grabberPlayerClient{config: config}
}

//
//oggFile, err := oggwriter.New("output.ogg", 48000, 2)
//if err != nil {
//panic(err)
//}
//ivfFile, err := ivfwriter.New("output.ivf")
//if err != nil {
//panic(err)
//}
