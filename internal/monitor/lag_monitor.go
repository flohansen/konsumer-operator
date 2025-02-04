package monitor

import (
	"context"
	"errors"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

var (
	ErrAlreadyStarted = errors.New("monitor has already been started")
)

type LagMonitor struct {
	cancel context.CancelFunc
}

// Starts the monitoring loop. If it has been already started, another call of
// this method will will return ErrAlreadyStarted.
func (m *LagMonitor) Start(ctx context.Context, groupID string, topic string) error {
	if m.cancel != nil {
		return ErrAlreadyStarted
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel

	log := log.FromContext(ctx)
	timer := time.NewTicker(time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			log.Info("monitor", "group", groupID, "topic", topic)
		}
	}
}

// Stops the monitor.
func (m *LagMonitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}
