package domain

import (
	"errors"
	"time"
)

var (
	ErrGrabberNotFound      = errors.New("grabber not found")
	ErrGrabberAlreadyExists = errors.New("grabber already exists")
)

type Grabber struct {
	ID               string
	Name             string
	LastPing         time.Time
	ConnectionsCount int
	StreamTypes      []string
	CurrentRecordId  string
}

func (g *Grabber) IsActive(timeout time.Duration) bool {
	if g.LastPing.IsZero() {
		return false
	}
	return time.Since(g.LastPing) < timeout
}

type GrabberRepository interface {
	Save(grabber Grabber) error
	GetByID(id string) (Grabber, error)
	GetByName(name string) (Grabber, error)
	GetAll() ([]Grabber, error)
	Delete(id string) error
	DeleteStale(timeout time.Duration) error
}
