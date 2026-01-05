package memory

import (
	"sync"
	"time"

	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/domain"
)

type GrabberRepository struct {
	grabbers map[string]domain.Grabber
	mu       sync.RWMutex
}

func NewGrabberRepository() *GrabberRepository {
	return &GrabberRepository{
		grabbers: make(map[string]domain.Grabber),
	}
}

func (r *GrabberRepository) Save(g domain.Grabber) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.grabbers[g.ID] = g
	return nil
}

func (r *GrabberRepository) GetByID(id string) (domain.Grabber, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	g, ok := r.grabbers[id]
	if !ok {
		return domain.Grabber{}, domain.ErrGrabberNotFound
	}
	return g, nil
}

func (r *GrabberRepository) GetByName(name string) (domain.Grabber, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var found domain.Grabber
	var isFound bool

	for _, g := range r.grabbers {
		if g.Name != name {
			continue
		}

		if !isFound || (g.LastPing.After(found.LastPing)) {
			found = g
			isFound = true
		}
	}

	if !isFound {
		return domain.Grabber{}, domain.ErrGrabberNotFound
	}

	return found, nil
}

func (r *GrabberRepository) GetAll() ([]domain.Grabber, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	grabbers := make([]domain.Grabber, 0, len(r.grabbers))
	for _, g := range r.grabbers {
		grabbers = append(grabbers, g)
	}
	return grabbers, nil
}

func (r *GrabberRepository) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.grabbers[id]; !ok {
		return domain.ErrGrabberNotFound
	}

	delete(r.grabbers, id)
	return nil
}

func (r *GrabberRepository) DeleteStale(timeout time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	idsToDelete := make([]string, 0)
	for id, g := range r.grabbers {
		if !g.LastPing.IsZero() && time.Since(g.LastPing) > timeout {
			idsToDelete = append(idsToDelete, id)
		}
	}

	for _, id := range idsToDelete {
		delete(r.grabbers, id)
	}

	return nil
}
