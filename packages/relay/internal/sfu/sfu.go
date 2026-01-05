package sfu

import (
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
)

type SFU interface {
	domain.MediaProvider
}
