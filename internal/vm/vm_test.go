package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStaticAddress(t *testing.T) {
	cases := []struct {
		name   string
		base   string
		offset int
		want   string
	}{
		{"node-1", "192.168.122", 100, "192.168.122.101"},
		{"vm-02", "192.168.123", 100, "192.168.123.102"},
		{"node-50", "192.168.122", 50, "192.168.122.100"},
	}
	for _, tc := range cases {
		got, err := staticAddress(tc.name, tc.base, tc.offset)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}

	got, err := staticAddress("plain", "192.168.122", 100)
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	if !strings.HasPrefix(got, "192.168.122.1") {
		t.Errorf("hashed name %q not in offset pool", got)
	}

	if _, err := staticAddress("node-200", "192.168.122", 100); err == nil {
		t.Error("expected error for out-of-range address")
	}
}

func TestDeterministicMAC(t *testing.T) {
	a := deterministicMAC("vm-01")
	if a != deterministicMAC("vm-01") {
		t.Error("MAC is not deterministic")
	}
	if !strings.HasPrefix(a, "52:54:00:c1:2e:") {
		t.Errorf("unexpected MAC %s", a)
	}
}

func TestQcow2CacheEnsure(t *testing.T) {
	payload := []byte("fake qcow2 bytes")
	sum := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	var hits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/artifacts/edge.qcow2", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write(payload)
	})
	mux.HandleFunc("/artifacts/edge.qcow2/sha256", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, digest)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	url := server.URL + "/artifacts/edge.qcow2"

	cache := NewQcow2Cache(t.TempDir())
	path, err := cache.Ensure(url, "")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cached file: %v", err)
	}
	if string(got) != string(payload) {
		t.Error("cached bytes differ from payload")
	}
	if !strings.Contains(path, strings.TrimPrefix(digest, "sha256:")) {
		t.Errorf("cache path %s not keyed by digest", path)
	}

	before := hits.Load()
	if _, err := cache.Ensure(url, digest); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if hits.Load() != before {
		t.Error("cached file was re-downloaded")
	}
}

func TestQcow2CacheDigestMismatch(t *testing.T) {
	payload := []byte("actual bytes")
	other := sha256.Sum256([]byte("expected bytes"))
	digest := "sha256:" + hex.EncodeToString(other[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/artifacts/edge.qcow2", func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cache := NewQcow2Cache(t.TempDir())
	if _, err := cache.Ensure(server.URL+"/artifacts/edge.qcow2", digest); err == nil {
		t.Fatal("expected digest mismatch error")
	}
	if _, err := os.Stat(cache.cachedPath(digest)); !os.IsNotExist(err) {
		t.Error("mismatched download was cached")
	}
}

func TestBuildSeed(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso not installed")
	}
	dir := t.TempDir()
	seed, err := BuildSeed(SeedSpec{
		Dir:     dir,
		Domain:  "vm-01",
		MAC:     deterministicMAC("vm-01"),
		Address: "192.168.122.101",
		Gateway: "192.168.122.1",
	})
	if err != nil {
		t.Fatalf("build seed: %v", err)
	}
	info, err := os.Stat(seed)
	if err != nil || info.Size() == 0 {
		t.Fatalf("seed.iso missing or empty: %v", err)
	}
	userData, err := os.ReadFile(filepath.Join(dir, "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(userData), "/var/lib/c12s/node.env") {
		t.Error("user-data does not write node.env")
	}
	// control-plane addresses come from the image, not from the seed
	if strings.Contains(string(userData), "NATS_ADDRESS") {
		t.Error("user-data sets NATS_ADDRESS without an override")
	}
	networkConfig, err := os.ReadFile(filepath.Join(dir, "network-config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(networkConfig), `mac_address: "52:54:00:c1:2e:`) {
		t.Error("network-config missing deterministic MAC match")
	}
}

func TestNodeEnvLinesOverride(t *testing.T) {
	got := strings.Join(nodeEnvLines("10.0.0.1:4222", map[string]string{"B": "2", "A": "1"}), "\n")
	for _, want := range []string{"NATS_ADDRESS=10.0.0.1:4222", "A=1\nB=2"} {
		if !strings.Contains(got, want) {
			t.Errorf("node env missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildSeedValidates(t *testing.T) {
	if _, err := BuildSeed(SeedSpec{Dir: t.TempDir(), Domain: "vm-01"}); err == nil {
		t.Error("expected error for missing mac/address/nats")
	}
}
