package services

import (
	"context"
	"log"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
)

const heartbeatTimeout = 10 * time.Second

// HeartbeatService reports liveness for every node, regardless of which
// modules are running. It exists so that (a) Magnetar can tell stale records
// from live ones and GC the former, and (b) nodes without the bootc module —
// which have no other periodic control-plane call — still learn that the
// control plane lost their record and must re-register.
type HeartbeatService struct {
	client    *magnetarapi.NodeHeartbeatAsyncClient
	nodeId    string
	interval  time.Duration
	onUnknown func()
}

func NewHeartbeatService(client *magnetarapi.NodeHeartbeatAsyncClient, nodeId string, interval time.Duration) *HeartbeatService {
	return &HeartbeatService{
		client:   client,
		nodeId:   nodeId,
		interval: interval,
	}
}

// SetUnknownNodeHandler is called once when Magnetar answers that it has no
// record of this node (e.g. the control plane was reset).
func (h *HeartbeatService) SetUnknownNodeHandler(handler func()) {
	h.onUnknown = handler
}

// Run beats until ctx is done. The first beat is sent after one interval, so
// startup (which already probes the control plane) is not doubled.
func (h *HeartbeatService) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beatCtx, cancel := context.WithTimeout(context.Background(), heartbeatTimeout)
			found, err := h.client.Heartbeat(beatCtx, h.nodeId)
			cancel()
			if err != nil {
				log.Printf("[heartbeat] failed: %s", err)
				continue
			}
			if !found {
				log.Println("[heartbeat] node unknown to Magnetar, re-registering")
				if h.onUnknown != nil {
					h.onUnknown()
				}
				return
			}
		}
	}
}
