// Package sdpconv converts between the wire-format SDP/ICE types in
// internal/api (which has no pion dependency) and the pion runtime types.
// Anything that handles WebRTC objects directly (SFU, test harnesses,
// reference clients) lives on the pion side and uses these helpers at
// the JSON boundary.
package sdpconv

import (
	"github.com/pion/webrtc/v4"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
)

func ToPionSDP(s api.SDP) webrtc.SessionDescription {
	var t webrtc.SDPType
	switch s.Type {
	case "offer":
		t = webrtc.SDPTypeOffer
	case "answer":
		t = webrtc.SDPTypeAnswer
	case "pranswer":
		t = webrtc.SDPTypePranswer
	case "rollback":
		t = webrtc.SDPTypeRollback
	}
	return webrtc.SessionDescription{Type: t, SDP: s.SDP}
}

func FromPionSDP(s webrtc.SessionDescription) api.SDP {
	return api.SDP{Type: s.Type.String(), SDP: s.SDP}
}

func ToPionICE(c api.ICECandidate) webrtc.ICECandidateInit {
	return webrtc.ICECandidateInit{
		Candidate:        c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}
}

func FromPionICE(c webrtc.ICECandidateInit) api.ICECandidate {
	return api.ICECandidate{
		Candidate:        c.Candidate,
		SDPMid:           c.SDPMid,
		SDPMLineIndex:    c.SDPMLineIndex,
		UsernameFragment: c.UsernameFragment,
	}
}
