package startup

import (
	"os"
	"path/filepath"
	"testing"
)

// run_vms needs both a libvirt daemon and hardware virtualization: libvirt
// is installed on every image, so the socket alone made every node (even
// VMs without nested KVM) claim it.
func TestCanRunVMs(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "virtqemud-sock")
	kvm := filepath.Join(dir, "kvm")
	touch := func(p string) {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if canRunVMs([]string{sock}, kvm) {
		t.Fatal("neither socket nor kvm: must be false")
	}
	touch(sock)
	if canRunVMs([]string{sock}, kvm) {
		t.Fatal("libvirt without /dev/kvm: must be false")
	}
	touch(kvm)
	if !canRunVMs([]string{filepath.Join(dir, "missing"), sock}, kvm) {
		t.Fatal("libvirt and kvm: must be true")
	}
}
