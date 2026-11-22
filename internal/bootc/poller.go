package bootc

import (
	"context"
	"log"
	"os"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
)

type StatusCallback func(phase string, message string)

type Poller struct {
	interval      time.Duration
	desired       *DesiredStore
	attempts      *AttemptStore
	statusHandler StatusCallback
	source        DesiredSource
	nodeId        string
	imageRegistry string // completes short image refs (IMAGE_REGISTRY)
	probe         func() *Status
}

func NewPoller(interval time.Duration, stateDir string, statusHandler StatusCallback) *Poller {
	return &Poller{
		interval:      interval,
		desired:       NewDesiredStore(stateDir),
		attempts:      NewAttemptStore(stateDir),
		statusHandler: statusHandler,
		probe:         ProbeStatus,
	}
}

// SetImageRegistry sets the registry short image refs are pulled from.
func (p *Poller) SetImageRegistry(registry string) {
	p.imageRegistry = registry
}

func (p *Poller) Desired() *DesiredStore {
	return p.desired
}

func (p *Poller) Attempts() *AttemptStore {
	return p.attempts
}

func (p *Poller) SetStatusHandler(handler StatusCallback) {
	p.statusHandler = handler
}

func (p *Poller) report(phase string, message string) {
	if p.statusHandler != nil {
		p.statusHandler(phase, message)
	} else {
		log.Printf("os status [%s]: %s", phase, message)
	}
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	p.reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.reconcile()
		}
	}
}

func (p *Poller) reconcile() {
	p.pullDesired()
	desired := p.desired.Get()
	status := p.probe()
	if status == nil {
		p.report(magnetarapi.OsPhaseDegraded, "bootc status unavailable")
		return
	}
	if desired == nil {
		// nothing to reconcile, but keep the control plane's view current:
		// after `image rollback` it would otherwise show the old image
		p.report(magnetarapi.OsPhaseUnmanaged, "no desired image declared; running "+status.Image+"@"+status.Digest)
		return
	}
	bootID := CurrentBootID()
	last := p.attempts.Last()
	restaged := false

	if status.Digest == desired.Digest {
		p.reconcileBooted(desired, last, bootID)
		return
	}

	if last != nil && last.Digest == desired.Digest {
		switch last.Result {
		case magnetarapi.OsAttemptApplied:
			if last.BootID != "" && bootID == last.BootID {
				// The deployment is staged but this process still runs on
				// the pre-switch boot: the reboot has not landed yet. Do
				// not infer a rollback from the old digest here (that would
				// poison the attempt) and do not switch again.
				p.report(magnetarapi.OsPhaseSwitching, "deployment staged, waiting for reboot")
				return
			}
			returned := returnedFrom(status, last, desired)
			switch returned {
			case returnRolledBack, returnLostTwice:
				// The switch was performed and the node rebooted, but it
				// came back on the pre-switch image: the health gate
				// (greenboot) rejected the new deployment and rolled back.
				// Park instead of looping.
				message := "greenboot rolled back the desired digest, waiting for new desired image"
				if returned == returnLostTwice {
					message = "staged deployment was lost twice before it was finalized, waiting for new desired image"
				}
				_ = p.attempts.Record(Attempt{
					Image:          desired.ImageRef,
					Digest:         desired.Digest,
					PreviousDigest: last.PreviousDigest,
					Result:         magnetarapi.OsAttemptRolledBack,
					Timestamp:      time.Now().Unix(),
					BootID:         last.BootID,
				})
				p.report(magnetarapi.OsPhaseRolledBack, message)
				return
			case returnLostStaging:
				// The shutdown was cut short before ostree finalized the
				// staged deployment (e.g. the VM's host rebooted at the same
				// time): the new image was never booted, nothing to park.
				p.report(magnetarapi.OsPhaseDegraded, "staged deployment was lost before it was finalized, switching again")
				restaged = true
				_ = p.attempts.Clear()
			case returnDrift:
				// The node came back on some *other* image: an out-of-band
				// change (manual bootc switch), not a greenboot rollback.
				// The declared state wins — fall through and switch back.
				p.report(magnetarapi.OsPhaseDegraded, "drift detected: booted "+status.Digest+", reconciling to desired image")
				_ = p.attempts.Clear()
			}
		case magnetarapi.OsAttemptRolledBack:
			p.report(magnetarapi.OsPhaseRolledBack, "previous attempt for desired digest was rolled back, waiting for new desired image")
			return
		case magnetarapi.OsAttemptFailed:
			_ = p.attempts.Clear()
		}
	}

	// the stored wish keeps the short ref; only the pull needs the registry
	pullRef, err := ResolveImageRef(desired.ImageRef, p.imageRegistry)
	if err != nil {
		_ = p.attempts.Record(Attempt{
			Image:     desired.ImageRef,
			Digest:    desired.Digest,
			Result:    magnetarapi.OsAttemptFailed,
			Timestamp: time.Now().Unix(),
		})
		p.report(magnetarapi.OsPhaseDegraded, err.Error())
		return
	}
	if err := Verify(pullRef, desired.Digest); err != nil {
		_ = p.attempts.Record(Attempt{
			Image:     desired.ImageRef,
			Digest:    desired.Digest,
			Result:    magnetarapi.OsAttemptFailed,
			Timestamp: time.Now().Unix(),
		})
		p.report(magnetarapi.OsPhaseDegraded, "verification failed: "+err.Error())
		return
	}
	p.report(magnetarapi.OsPhaseSwitching, "switching to "+pullRef+"@"+desired.Digest)
	if err := Switch(pullRef, desired.Digest); err != nil {
		_ = p.attempts.Record(Attempt{
			Image:     desired.ImageRef,
			Digest:    desired.Digest,
			Result:    magnetarapi.OsAttemptFailed,
			Timestamp: time.Now().Unix(),
		})
		p.report(magnetarapi.OsPhaseDegraded, err.Error())
		return
	}
	// record before rebooting (see Switch doc): this is what lets the next
	// boot tell "we switched and came back on the old image" (rollback)
	// apart from "we never switched" (and from drift to a third image).
	// BootID pins the record to the boot that staged the deployment.
	_ = p.attempts.Record(Attempt{
		Image:          desired.ImageRef,
		Digest:         desired.Digest,
		PreviousDigest: status.Digest,
		Result:         magnetarapi.OsAttemptApplied,
		Timestamp:      time.Now().Unix(),
		BootID:         bootID,
		Restaged:       restaged,
	})
	if desired.RebootPolicy != "" && desired.RebootPolicy != "immediate" {
		p.report(magnetarapi.OsPhaseDegraded, "staged switch, reboot policy "+desired.RebootPolicy+" not supported yet")
		return
	}
	if err := Reboot(); err != nil {
		p.report(magnetarapi.OsPhaseDegraded, "reboot after switch failed: "+err.Error())
	}
}

// reconcileBooted handles the "booted digest matches the desired digest"
// case. Digest equality alone is not proof of health: a deployment that just
// passed greenboot's gate reports healthy, while a deployment whose gate has
// not run yet (or has failed) stays in switching until greenboot accepts it
// or rolls it back.
//
// The gate fires on every switch-reboot when greenboot is present: we wait
// until greenboot-healthcheck.service has run on this boot AND boot_success=1.
// boot_success alone is not enough: it is reset to 0 by greenboot's GRUB
// snippet at boot and set to 1 only when the checks pass, so before the
// check has run it can still hold the previous boot's "1".
func (p *Poller) reconcileBooted(desired *DesiredImage, last *Attempt, bootID string) {
	if last != nil && last.Digest == desired.Digest {
		if last.Result == magnetarapi.OsAttemptApplied {
			rebooted := last.BootID == "" || bootID != last.BootID
			if rebooted && GreenbootPresent() {
				// Wait until greenboot-healthcheck.service has completed
				// its run on this boot. While it is still "activating",
				// report switching — regardless of what boot_success says
				// (it may be stale from the previous deployment).
				if !GreenbootHealthcheckDone() {
					p.report(magnetarapi.OsPhaseSwitching, "booted desired image, waiting for greenboot health check to finish")
					return
				}
				// Healthcheck finished. Check the verdict: boot_success=1
				// means greenboot accepted this deployment.
				if value, ok := GrubEnvValue("boot_success"); !ok || value != "1" {
					p.report(magnetarapi.OsPhaseSwitching, "booted desired image, greenboot health check did not pass")
					return
				}
			}
		}
		_ = p.attempts.Clear()
	}
	p.report(magnetarapi.OsPhaseHealthy, "booted image matches desired state")
}

func StateDir() string {
	dir := os.Getenv("NODE_ID_DIR_PATH")
	if dir == "" {
		dir = "/var/lib/c12s"
	}
	return dir
}

type returnKind int

const (
	returnRolledBack  returnKind = iota // greenboot rejected the new image
	returnLostStaging                   // staged deployment never reached disk
	returnLostTwice                     // ... again, after one re-switch
	returnDrift                         // some third image: out-of-band change
)

// returnedFrom classifies a boot after an applied switch that did not land on
// the desired digest. Back on the pre-switch image means either greenboot
// rolled back — then the rejected image is still on disk as the rollback
// deployment — or the staging shutdown was cut short before ostree finalized
// the deployment, and the new image was never booted.
func returnedFrom(status *Status, last *Attempt, desired *DesiredImage) returnKind {
	if last.PreviousDigest != "" && status.Digest != last.PreviousDigest {
		return returnDrift
	}
	if status.RollbackDigest == desired.Digest {
		return returnRolledBack
	}
	if last.Restaged {
		return returnLostTwice
	}
	return returnLostStaging
}
