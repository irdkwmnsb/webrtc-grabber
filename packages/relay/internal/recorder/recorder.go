package recorder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/player_client"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

type Recorder interface {
	Record(ctx context.Context, key string, peerName string, streamType string, time time.Duration) (string, error)
	StopRecord(recordId string)
}

type recorder struct {
	config               Config
	maxRecordingDuration time.Duration
	mx                   sync.Locker
	recordings           []*recordingInfo
}

type recordingInfo struct {
	id         string
	peerName   string
	streamType string
	duration   time.Duration
	cancelFunc context.CancelFunc
}

func (r *recorder) Record(ctx context.Context, key string, peerName string, streamType string, duration time.Duration) (string, error) {
	recordingId := fmt.Sprintf("%s_%s_%s", time.Now().Format("2006_01_02_15_04_05"), peerName, streamType)
	if key != "" {
		recordingId += "_" + key
	}
	recordingDuration := r.chooseDuration(duration)
	innerCtx, cancelFunc := context.WithTimeout(ctx, recordingDuration)
	rec := recordingInfo{
		id:         recordingId,
		cancelFunc: cancelFunc,
		peerName:   peerName,
		streamType: streamType,
		duration:   recordingDuration,
	}
	go func() {
		r.recordBackground(innerCtx, &rec)
	}()

	r.mx.Lock()
	defer r.mx.Unlock()
	r.recordings = append(r.recordings, &rec)
	return recordingId, nil
}

func (r *recorder) chooseDuration(userDuration time.Duration) time.Duration {
	if userDuration == 0 {
		return r.maxRecordingDuration
	} else if userDuration < r.maxRecordingDuration {
		return userDuration
	} else {
		return r.maxRecordingDuration
	}
}

func (r *recorder) recordBackground(ctx context.Context, rec *recordingInfo) {
	client := player_client.NewClient(player_client.Config{SignallingUrl: r.config.SignallingUrl, Credential: r.config.SignallingCredential})

	var peerConnection *webrtc.PeerConnection
	defer func() {
		if peerConnection != nil {
			_ = peerConnection.Close()
		}
	}()

	cfg := player_client.ConnectionConfig{PeerName: rec.peerName, StreamType: rec.streamType}
	cfg.GetOffer = func(ctx player_client.ConnectionCtx) (offer webrtc.SessionDescription, err error) {
		if peerConnection != nil {
			return
		}

		slog.Debug("set ice config", "config", ctx.PCConfig.WebrtcConfiguration())
		peerConnection, err = webrtc.NewPeerConnection(ctx.PCConfig.WebrtcConfiguration())
		if err != nil {
			return
		}
		if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
			return
		} else if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
			return
		}

		offer, err = peerConnection.CreateOffer(nil)
		if err != nil {
			return
		}
		err = peerConnection.SetLocalDescription(offer)

		peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
			slog.Debug("peer connection state has changed", "state", s.String())

			if s == webrtc.PeerConnectionStateFailed {
				rec.cancelFunc()
			}
		})
		peerConnection.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			slog.Debug("sending ice candidate")
			if err := ctx.SendICECandidate(candidate); err != nil {
				slog.Error("failed to send ice candidate info", "error", err)
			}
		})
		peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
			rec.saveToDisk(r.config.RecordingsDirectory, track.Codec(), track)
		})

		return
	}
	cfg.OnOfferAnswer = func(ctx player_client.ConnectionCtx, answer webrtc.SessionDescription) error {
		if peerConnection == nil {
			return errors.New("no active webrtc connection to accept offer answer")
		}
		if peerConnection.ConnectionState() != webrtc.PeerConnectionStateNew {
			slog.Debug("peer connection already has offer answer")
			return nil
		}
		slog.Debug("setting answer")
		err := peerConnection.SetRemoteDescription(answer)
		return err
	}
	cfg.OnGrabberIce = func(ctx player_client.ConnectionCtx, ice webrtc.ICECandidateInit) error {
		if peerConnection == nil {
			return errors.New("no active webrtc connection to accept offer answer")
		}
		slog.Debug("setting ice")

		return peerConnection.AddICECandidate(ice)
	}

	err := client.ConnectToPeer(ctx, cfg)
	if err != nil {
		slog.Error("failed to connect to peer", "error", err)
		return
	}
}

func (rec *recordingInfo) saveToDisk(outputDir string, codec webrtc.RTPCodecParameters, track *webrtc.TrackRemote) {
	var writer media.Writer
	outputFileName := outputDir + string(os.PathSeparator) + rec.id
	var err error
	switch codec.MimeType {
	case webrtc.MimeTypeOpus:
		outputFileName += "_audio.ogg"
		writer, err = oggwriter.New(outputFileName, 48000, 2)
	case webrtc.MimeTypeVP8:
		outputFileName += ".ivf"
		writer, err = ivfwriter.New(outputFileName)
	case webrtc.MimeTypeH264:
		outputFileName += ".mp4"
		writer, err = ivfwriter.New(outputFileName)
	default:
		slog.Warn("failed to record track with unsupported mime type", "mimeType", codec.MimeType)
	}
	if err != nil {
		slog.Error("failed to create file", "filename", outputFileName, "error", err)
		return
	}

	slog.Info("got track, recording", "mimeType", codec.MimeType, "outputFile", outputFileName)

	defer func() {
		if err := writer.Close(); err != nil {
			slog.Error("failed to close record writer", "error", err)
		}
	}()

	for {
		rtpPacket, _, err := track.ReadRTP()
		if errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			slog.Error("failed to read frame while recording", "error", err)
		}
		if err := writer.WriteRTP(rtpPacket); err != nil {
			panic(err)
		}
	}
}

func (r *recorder) StopRecord(recordId string) {
	//TODO implement me
	panic("implement me")
}

func NewRecorder(config Config) Recorder {
	r := recorder{config: config}
	r.mx = &sync.Mutex{}
	r.maxRecordingDuration = time.Duration(r.config.MaxRecordDuration) * time.Second
	return &r
}
