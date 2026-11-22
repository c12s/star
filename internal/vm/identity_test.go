package vm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Same vector as cockpit's VM id test and Python's uuid.uuid5: cockpit and
// star must derive the same id for a VM independently.
func TestVMNodeID(t *testing.T) {
	got, err := VMNodeID("5c1e7d2a-1b3c-4d5e-8f90-123456789abc", "vm-01")
	if err != nil {
		t.Fatal(err)
	}
	if got != "f210cdd5-ce6a-561e-a06a-29929fc319bd" {
		t.Fatalf("VMNodeID = %s", got)
	}
	if _, err := VMNodeID("not-a-uuid", "vm-01"); err == nil {
		t.Fatal("expected error for a malformed host id")
	}
}

func TestBuildSeedIdentity(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso not installed")
	}
	dir := t.TempDir()
	_, err := BuildSeed(SeedSpec{
		Dir:     dir,
		Domain:  "vm-01",
		MAC:     deterministicMAC("vm-01"),
		Address: "192.168.123.101",
		Gateway: "192.168.123.1",
		NodeID:  "f210cdd5-ce6a-561e-a06a-29929fc319bd",
		Labels:  map[string]string{"vm-host": "5c1e7d2a-1b3c-4d5e-8f90-123456789abc", "vm-name": "vm-01"},
	})
	if err != nil {
		t.Fatal(err)
	}
	userData, err := os.ReadFile(filepath.Join(dir, "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"path: /var/lib/c12s/nodeid",
		"f210cdd5-ce6a-561e-a06a-29929fc319bd",
		"NODE_LABELS=vm-host=5c1e7d2a-1b3c-4d5e-8f90-123456789abc,vm-name=vm-01",
	} {
		if !strings.Contains(string(userData), want) {
			t.Errorf("user-data missing %q:\n%s", want, userData)
		}
	}
}

func TestDiskURL(t *testing.T) {
	cases := []struct{ disk, base, want string }{
		{"edge-base-v4.0.qcow2", "http://192.168.122.1:8081", "http://192.168.122.1:8081/artifacts/edge-base-v4.0.qcow2"},
		{"edge-base-v4.0.qcow2", "http://192.168.122.1:8081/", "http://192.168.122.1:8081/artifacts/edge-base-v4.0.qcow2"},
		{"http://other:8081/artifacts/x.qcow2", "", "http://other:8081/artifacts/x.qcow2"},
	}
	for _, c := range cases {
		got, err := diskURL(c.disk, c.base)
		if err != nil || got != c.want {
			t.Errorf("diskURL(%q, %q) = %q, %v; want %q", c.disk, c.base, got, err, c.want)
		}
	}
	if _, err := diskURL("x.qcow2", ""); err == nil {
		t.Error("a bare disk name without DISK_REGISTRY_URL must fail")
	}
}
