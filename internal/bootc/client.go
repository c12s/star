package bootc

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

const grubEnvPath = "/boot/grub2/grubenv"

type Status struct {
	Image   string
	Digest  string
	Version string
	// RollbackDigest is the other deployment on disk (empty if none). After
	// a greenboot rollback it holds the rejected image.
	RollbackDigest string
	RollbackImage  string
}

// bootc status --json returns the org.containers.bootc/v1 BootcHost schema.
// The booted deployment lives at .status.booted.image with the following shape:
//
//	"image": {
//	    "image": {"image": "<ref>", "transport": "registry"},
//	    "imageDigest": "sha256:...",
//	    "version": "..."
//	}
type bootcStatusOutput struct {
	Status *struct {
		Booted   *bootcStatusEntry `json:"booted"`
		Rollback *bootcStatusEntry `json:"rollback"`
	} `json:"status"`
}

type bootcStatusEntry struct {
	Image *bootcImageStatus `json:"image"`
}

type bootcImageStatus struct {
	Image       *bootcImageRef `json:"image"`
	ImageDigest string         `json:"imageDigest"`
	Version     string         `json:"version"`
}

type bootcImageRef struct {
	Image     string `json:"image"`
	Transport string `json:"transport"`
}

func ProbeStatus() *Status {
	out, err := exec.Command("bootc", "status", "--json").CombinedOutput()
	if err != nil {
		log.Printf("[bootc] status failed: %s (stderr: %s)", err, string(out))
		return nil
	}
	return parseStatus(out)
}

// parseStatus reads `bootc status --json`: the booted deployment and the
// rollback entry.
func parseStatus(out []byte) *Status {
	parsed := &bootcStatusOutput{}
	if err := json.Unmarshal(out, parsed); err != nil {
		log.Printf("[bootc] status JSON parse failed: %s", err)
		return nil
	}
	if parsed.Status == nil || parsed.Status.Booted == nil {
		log.Println("[bootc] status: no booted deployment found")
		return nil
	}
	booted := parsed.Status.Booted
	if booted.Image == nil {
		return nil
	}
	status := &Status{
		Digest:  booted.Image.ImageDigest,
		Version: booted.Image.Version,
	}
	if booted.Image.Image != nil {
		status.Image = booted.Image.Image.Image
	}
	if rollback := parsed.Status.Rollback; rollback != nil && rollback.Image != nil {
		status.RollbackDigest = rollback.Image.ImageDigest
		if rollback.Image.Image != nil {
			status.RollbackImage = rollback.Image.Image.Image
		}
	}
	return status
}

func Switch(imageRef, digest string) error {
	// Stage the deployment without --apply: the caller records the attempt
	// BEFORE rebooting, so the loop guard knows a switch was performed even
	// if the reboot kills this process mid-flight.
	//
	// Rollback is greenboot's: greenboot-set-rollback-trigger.service
	// (RequiredBy=ostree-finalize-staged) runs on the staging shutdown and
	// sets boot_counter=GREENBOOT_MAX_BOOT_ATTEMPTS plus
	// greenboot_next_deployment_id. The 08_greenboot.cfg GRUB snippet counts
	// boot_counter down each boot and boots the previous entry at 0; the
	// health check reboots on failure and makes the fallback permanent.
	ref := fmt.Sprintf("%s@%s", imageRef, digest)
	out, err := exec.Command("bootc", "switch", ref).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootc switch failed: %s", string(out))
	}
	return nil
}

func Rollback() error {
	out, err := exec.Command("bootc", "rollback").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bootc rollback failed: %s", string(out))
	}
	return Reboot()
}

// CurrentBootID returns the kernel boot id, which changes on every boot.
// Empty string means "unknown" (callers treat that as "assume rebooted").
func CurrentBootID() string {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// GreenbootPresent reports whether greenboot's health gate is installed. The
// binary is not in PATH (/usr/libexec/greenboot/greenboot).
func GreenbootPresent() bool {
	_, err := os.Stat("/usr/libexec/greenboot/greenboot")
	return err == nil
}

// GreenbootHealthcheckDone reports whether greenboot-healthcheck.service has
// finished its run on this boot. The caller uses this to avoid trusting a
// stale boot_success=1 left in grubenv from a previous good boot.
func GreenbootHealthcheckDone() bool {
	// is-active exits non-zero for anything but "active"; the state is
	// still printed, so the error carries no extra information
	out, _ := exec.Command("systemctl", "is-active", "greenboot-healthcheck.service").Output()
	return healthcheckFinished(strings.TrimSpace(string(out)))
}

// healthcheckFinished maps the oneshot unit's state: "active" (RemainAfterExit)
// means the checks passed this boot, "failed" that they failed. "inactive"
// means the unit has not run yet: star starts before greenboot, and treating
// that as finished reported healthy on images greenboot later rejected.
func healthcheckFinished(state string) bool {
	return state == "active" || state == "failed"
}

// GrubEnvValue reads a single key from the GRUB environment block.
func GrubEnvValue(key string) (string, bool) {
	data, err := os.ReadFile(grubEnvPath)
	if err != nil {
		return "", false
	}
	prefix := key + "="
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\x00")
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix), true
		}
	}
	return "", false
}

func Reboot() error {
	return exec.Command("systemctl", "reboot").Run()
}
