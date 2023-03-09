package player_client

import (
	"errors"
	"github.com/fasthttp/websocket"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/pion/webrtc/v3"
	"log"
	"strings"
)

type Config struct {
	SignallingUrl string
	Credential    string
}

type Client interface {
	ConnectToPeer(PeerName string, streamType string) error
}

type grabberPlayerClient struct {
	config Config
}

func (g *grabberPlayerClient) ConnectToPeer(peerName string, streamType string) error {
	log.Printf("connecting to %s", g.getWebSocketClientUrl())
	c, _, err := websocket.DefaultDialer.Dial(g.getWebSocketClientUrl(), nil)
	if err != nil {
		return err
	}
	if err := c.WriteJSON(api.PlayerMessage{
		Event:      api.PlayerMessageEventAuth,
		PlayerAuth: &api.PlayerAuthMessage{Credential: g.config.Credential},
	}); err != nil {
		return err
	}
	var initPeerMessage api.PlayerMessage
	if err := c.ReadJSON(&initPeerMessage); err != nil {
		return err
	} else if initPeerMessage.Event != api.PlayerMessageEventInitPeer || initPeerMessage.InitPeer == nil {
		return errors.New("grabber_client: no init peer message")
	}

	config := parseWebrtcConfiguration(initPeerMessage.InitPeer.PcConfig)
	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer func() { _ = peerConnection.Close() }()

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		return err
	} else if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		return err
	}

	createdOffer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err = c.WriteJSON(api.PlayerMessage{
		Event: api.PlayerMessageEventOffer,
		Offer: &api.OfferMessage{PeerName: &peerName, StreamType: streamType, Offer: createdOffer},
	}); err != nil {
		return err
	}

	return nil
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
