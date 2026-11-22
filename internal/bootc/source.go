package bootc

import (
	"context"
	"log"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
)

// DesiredSource is where the operator's declared image lives: Magnetar,
// answered over NATS (magnetar.os.desired.get).
type DesiredSource interface {
	GetDesiredOsImage(ctx context.Context, nodeId string) (*magnetarapi.DesiredOsImageGetResp, error)
}

const desiredPullTimeout = 5 * time.Second

// SetDesiredSource makes the poller pull the declared image on every tick.
// Without a source the node only knows what os_update pushes delivered.
func (p *Poller) SetDesiredSource(source DesiredSource, nodeId string) {
	p.source = source
	p.nodeId = nodeId
}

// pullDesired brings the local desired image in line with the control plane.
// The control plane wins when it answers; with no answer (Magnetar down,
// network partition) the node keeps converging on its local copy instead of
// acting on a guess.
func (p *Poller) pullDesired() {
	if p.source == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), desiredPullTimeout)
	defer cancel()
	resp, err := p.source.GetDesiredOsImage(ctx, p.nodeId)
	if err != nil {
		log.Printf("[os] desired image unavailable from control plane, using local copy: %s", err)
		return
	}
	if resp.NotFound {
		// unknown node: the reporter restarts star to re-register
		return
	}
	next := desiredFromProto(resp.Image)
	local := p.desired.Get()
	switch {
	case next == nil && local == nil:
	case next == nil:
		log.Printf("[os] control plane declares no image, dropping local desired %s@%s", local.ImageRef, local.Digest)
		p.desired.Clear()
	case local == nil || *local != *next:
		log.Printf("[os] desired image from control plane: %s@%s (reboot policy: %s)", next.ImageRef, next.Digest, next.RebootPolicy)
		p.adopt(*next)
	}
}

// adopt sets a newly declared image. Declaring a digest whose last attempt
// was rolled back is the operator asking to retry it, so the park is lifted.
func (p *Poller) adopt(desired DesiredImage) {
	if last := p.attempts.Last(); last != nil && last.Digest == desired.Digest && last.Result == magnetarapi.OsAttemptRolledBack {
		log.Println("[os] rolled-back digest declared again, clearing parked attempt")
		_ = p.attempts.Clear()
	}
	p.desired.Set(desired)
}

func desiredFromProto(image *magnetarapi.DesiredOsImage) *DesiredImage {
	if image == nil || image.Digest == "" {
		return nil
	}
	return &DesiredImage{
		ImageRef:     image.ImageRef,
		Digest:       image.Digest,
		RebootPolicy: image.RebootPolicy,
	}
}
