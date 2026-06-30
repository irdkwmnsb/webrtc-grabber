package signalling

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/basicauth"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/proctoring"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

type adminProctoringPeer struct {
	PeerName          string                      `json:"peerName"`
	StreamKey         string                      `json:"streamKey"`
	TeamName          string                      `json:"teamName,omitempty"`
	University        string                      `json:"university,omitempty"`
	Online            bool                        `json:"online"`
	ActivelyStreaming bool                        `json:"activelyStreaming"`
	CommittedSeq      int64                       `json:"committedSeq"`
	TotalBytes        int64                       `json:"totalBytes"`
	SegmentCount      int                         `json:"segmentCount"`
	CurrentSegment    int                         `json:"currentSegment"`
	LastChunkAt       *time.Time                  `json:"lastChunkAt,omitempty"`
	Finalized         bool                        `json:"finalized"`
	Health            *api.ProctoringStreamHealth `json:"health,omitempty"`
}

type adminProctoringResponse struct {
	State    proctoring.State         `json:"state"`
	Peers    []adminProctoringPeer    `json:"peers"`
	Sessions []adminProctoringSession `json:"sessions"`
}

type adminProctoringSession struct {
	SessionId string `json:"sessionId"`
	Finalized bool   `json:"finalized"`
	IsActive  bool   `json:"isActive"`
}

func (s *Server) setupAdminApi() {
	s.app.Route("/api/admin", func(router fiber.Router) {
		router.Use(basicauth.New(basicauth.Config{
			Realm: "Forbidden",
			Authorizer: func(user, pass string) bool {
				return s.config.Security.PlayerCredential == nil || user == "admin" && pass == *s.config.Security.PlayerCredential
			},
		}))

		router.Get("/sitechecks", func(c *fiber.Ctx) error {
			return c.JSON(s.buildSiteCheckStatus())
		})

		router.Post("/record_start", func(c *fiber.Ctx) error {
			var req startRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			recordTimeout := s.config.Record.Timeout
			if req.Timeout != nil {
				recordTimeout = min(*req.Timeout, recordTimeout)
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event: api.GrabberMessageEventRecordStart,
				RecordStart: &api.RecordStartMessage{
					RecordId:    req.RecordId,
					TimeoutMsec: recordTimeout,
					UploadToken: s.signUploadToken(recordScope(req.PeerName)),
				}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send start recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_stop", func(c *fiber.Ctx) error {
			var req stopRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event:      api.GrabberMessageEventRecordStop,
				RecordStop: &api.RecordStopMessage{RecordId: req.RecordId}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send stop recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/record_upload", func(c *fiber.Ctx) error {
			var req uploadRecordRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(req.PeerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{
				Event:        api.GrabberMessageEventRecordUpload,
				RecordUpload: &api.RecordUploadMessage{RecordId: req.RecordId}})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send stop recoding request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/players_disconnect/:peerName", func(c *fiber.Ctx) error {
			peerName := c.Params("peerName")

			var socket sockets.Socket = nil
			if peer, ok := s.storage.getPeerByName(peerName); ok {
				socket = s.grabberSockets.GetSocket(peer.SocketId)
			}
			if socket == nil {
				return c.Status(fiber.StatusNotFound).SendString("Peer not found")
			}

			err := socket.WriteJSON(api.GrabberMessage{Event: api.GrabberMessageEventPlayersDisconnect})
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to send disconnect players request")
			}
			return c.Status(fiber.StatusOK).SendString("Ok")
		})

		router.Post("/proctoring/start", func(c *fiber.Ctx) error {
			var req api.ProctoringRequest
			if err := c.BodyParser(&req); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Bad Request")
			}

			err := s.proctoring.Start(proctoring.StartConfig{
				EndsAt:          req.EndsAt,
				ChunkDurationMs: req.ChunkDurationMs,
				Fps:             req.Fps,
				VideoBitrate:    req.VideoBitrate,
			})

			if errors.Is(err, proctoring.ErrAlreadyActive) {
				return c.Status(fiber.StatusConflict).SendString(err.Error())
			}

			if errors.Is(err, proctoring.ErrInvalidConfig) {
				return c.Status(fiber.StatusBadRequest).SendString(err.Error())
			}

			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
			}

			return c.JSON(s.proctoring.Get())
		})

		router.Post("/proctoring/pause", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Pause, s.proctoring.Get)
		})

		router.Post("/proctoring/resume", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Resume, s.proctoring.Get)
		})

		router.Post("/proctoring/stop", func(c *fiber.Ctx) error {
			return proctoringAction(c, s.proctoring.Stop, s.proctoring.Get)
		})

		router.Get("/proctoring", func(c *fiber.Ctx) error {
			state := s.proctoring.Get()
			resp := adminProctoringResponse{
				State:    state,
				Peers:    []adminProctoringPeer{},
				Sessions: []adminProctoringSession{},
			}

			if s.config.Record.StorageDir != "" {
				base := filepath.Join(s.config.Record.StorageDir, "proctoring")
				if entries, err := os.ReadDir(base); err == nil {
					for _, e := range entries {
						if !e.IsDir() {
							continue
						}
						sid := e.Name()
						resp.Sessions = append(resp.Sessions, adminProctoringSession{
							SessionId: sid,
							Finalized: isProctoringFinalized(s.config.Record.StorageDir, sid),
							IsActive:  sid == state.SessionId,
						})
					}
				}
			}

			online := map[string]*api.Peer{}
			for _, p := range s.storage.getAll() {
				peer := p
				online[p.Name] = &peer
			}
			meta := s.storage.participantMeta()

			seen := map[string]bool{}
			emit := func(peerName, streamKey string) {
				key := peerName + "/" + streamKey
				if seen[key] {
					return
				}
				seen[key] = true
				resp.Peers = append(resp.Peers, buildAdminPeer(s.config.Record.StorageDir, state.SessionId, peerName, streamKey, online[peerName], meta[peerName]))
			}

			if state.SessionId != "" && s.config.Record.StorageDir != "" {
				sessionDir := proctoringSessionDir(s.config.Record.StorageDir, state.SessionId)
				if peers, err := os.ReadDir(sessionDir); err == nil {
					for _, p := range peers {
						if !p.IsDir() {
							continue
						}
						peerDir := filepath.Join(sessionDir, p.Name())
						if streams, err := os.ReadDir(peerDir); err == nil {
							for _, st := range streams {
								if st.IsDir() {
									emit(p.Name(), st.Name())
								}
							}
						}
					}
				}
			}

			for name, peer := range online {
				for _, sk := range peer.ProctoringActiveStreams {
					emit(name, string(sk))
				}
			}

			return c.JSON(resp)
		})

		// Lists peers/streams for any session (active or past), reading the
		// session directory. Lets the proctoring page browse recordings that
		// the live /proctoring response (active session only) doesn't cover.
		router.Get("/proctoring/session/:sessionId", func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}
			sessionId := c.Params("sessionId")
			if !isSafePathSegment(sessionId) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid sessionId")
			}

			online := map[string]*api.Peer{}
			for _, p := range s.storage.getAll() {
				peer := p
				online[p.Name] = &peer
			}
			meta := s.storage.participantMeta()

			resp := adminProctoringResponse{
				State:    s.proctoring.Get(),
				Peers:    []adminProctoringPeer{},
				Sessions: []adminProctoringSession{},
			}
			sessionDir := proctoringSessionDir(s.config.Record.StorageDir, sessionId)
			if peers, err := os.ReadDir(sessionDir); err == nil {
				for _, p := range peers {
					if !p.IsDir() {
						continue
					}
					streams, err := os.ReadDir(filepath.Join(sessionDir, p.Name()))
					if err != nil {
						continue
					}
					for _, st := range streams {
						if st.IsDir() {
							resp.Peers = append(resp.Peers, buildAdminPeer(s.config.Record.StorageDir, sessionId, p.Name(), st.Name(), online[p.Name()], meta[p.Name()]))
						}
					}
				}
			}
			return c.JSON(resp)
		})

		router.Get("/proctoring/file/:sessionId/:peerName/:streamKey/:file", func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}
			sessionId := c.Params("sessionId")
			peerName := c.Params("peerName")
			streamKey := c.Params("streamKey")
			fileName := c.Params("file")
			if !isSafePathSegment(sessionId) || !isSafePathSegment(peerName) || !isSafePathSegment(fileName) || !isProctoringStreamKey(streamKey) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid params")
			}
			allowed := fileName == "full.webm" || fileName == "full.remuxed.webm" ||
				(strings.HasPrefix(fileName, "segment_") && strings.HasSuffix(fileName, ".webm"))
			if !allowed {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid file")
			}
			path := filepath.Join(s.config.Record.StorageDir, "proctoring", sessionId, peerName, streamKey, fileName)
			if _, err := os.Stat(path); err != nil {
				return c.Status(fiber.StatusNotFound).SendString("Not found")
			}
			c.Set("Content-Type", "video/webm")
			c.Set("Content-Disposition", `attachment; filename="`+sessionId+"_"+peerName+"_"+streamKey+"_"+fileName+`"`)
			return c.SendFile(path, false)
		})

		router.Post("/proctoring/finalize/:sessionId", func(c *fiber.Ctx) error {
			sessionId := c.Params("sessionId")
			if !isSafePathSegment(sessionId) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid sessionId")
			}
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}
			finalizeProctoringSession(s.config.Record.StorageDir, sessionId)
			return c.Status(fiber.StatusOK).SendString("OK")
		})

	})
}

func buildAdminPeer(storageDir, sessionId, peerName, streamKey string, online *api.Peer, info config.ParticipantInfo) adminProctoringPeer {
	out := adminProctoringPeer{
		PeerName:     peerName,
		StreamKey:    streamKey,
		TeamName:     info.TeamName,
		University:   info.University,
		CommittedSeq: -1,
		Finalized:    sessionId != "" && isProctoringFinalized(storageDir, sessionId),
	}
	if online != nil {
		out.Online = true
		for _, sk := range online.ProctoringActiveStreams {
			if string(sk) == streamKey {
				out.ActivelyStreaming = true
				break
			}
		}
		for _, h := range online.ProctoringHealth {
			if h.StreamKey == streamKey {
				hc := h
				out.Health = &hc
				break
			}
		}
	}
	if sessionId == "" {
		return out
	}
	stateFile := filepath.Join(storageDir, "proctoring", sessionId, peerName, streamKey, "state.json")
	if st, err := loadProctoringState(stateFile); err == nil {
		out.CommittedSeq = st.CommittedSeq
		out.TotalBytes = st.TotalBytes
		out.SegmentCount = len(st.Segments)
		if out.SegmentCount == 0 && st.TotalBytes > 0 {
			out.SegmentCount = 1 // legacy single-file session
		}
		out.CurrentSegment = st.CurrentSegment
	}
	full := filepath.Join(storageDir, "proctoring", sessionId, peerName, streamKey, "full.webm")
	if info, err := os.Stat(full); err == nil {
		t := info.ModTime()
		out.LastChunkAt = &t
	}
	return out
}

func proctoringAction(c *fiber.Ctx, action func() error, getter func() proctoring.State) error {
	err := action()
	switch {
	case errors.Is(err, proctoring.ErrNoSession),
		errors.Is(err, proctoring.ErrNotPaused),
		errors.Is(err, proctoring.ErrNotActive):
		return c.Status(fiber.StatusConflict).SendString(err.Error())
	case err != nil:
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	return c.JSON(getter())
}

type startRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
	Timeout  *uint  `json:"timeout"`
}

type stopRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
}

type uploadRecordRequest struct {
	PeerName string `json:"peerName"`
	RecordId string `json:"recordId"`
}
