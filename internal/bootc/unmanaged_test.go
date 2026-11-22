package bootc

import (
	"strings"
	"testing"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
)

// After `image rollback` the control plane declares no image. The node must
// still report what it booted, or Magnetar keeps showing the old image.
func TestReconcileReportsUnmanaged(t *testing.T) {
	var phase, message string
	p := NewPoller(0, t.TempDir(), func(ph, msg string) { phase, message = ph, msg })
	p.SetDesiredSource(&fakeSource{resp: &magnetarapi.DesiredOsImageGetResp{}}, "n1")
	p.probe = func() *Status {
		return &Status{Image: "192.168.122.1:5001/os/edge-base:v4.0", Digest: "sha256:old"}
	}
	p.reconcile()
	if phase != magnetarapi.OsPhaseUnmanaged {
		t.Fatalf("phase = %q, want %q", phase, magnetarapi.OsPhaseUnmanaged)
	}
	if !strings.Contains(message, "sha256:old") {
		t.Fatalf("message should name the booted image: %q", message)
	}
}
