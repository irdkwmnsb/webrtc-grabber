package utils

import (
	"time"
)

type IntervalTimer interface {
	Stop()
}

type timeInterval struct {
	quit chan<- struct{}
}

func (t *timeInterval) Stop() {
	t.quit <- struct{}{}
}

func SetIntervalTimer(duration time.Duration, function func()) IntervalTimer {
	ticker := time.NewTicker(duration)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				function()
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
	return &timeInterval{quit: quit}
}
