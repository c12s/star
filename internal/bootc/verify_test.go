package bootc

import "testing"

func TestPinnedRef(t *testing.T) {
	const d = "sha256:abc"
	for in, want := range map[string]string{
		"192.168.122.1:5001/os/edge-base:v3.10": "192.168.122.1:5001/os/edge-base@" + d,
		"192.168.122.1:5001/os/edge-base":       "192.168.122.1:5001/os/edge-base@" + d,
		"quay.io/fedora/fedora-bootc:45":        "quay.io/fedora/fedora-bootc@" + d,
		"registry/os/img@sha256:old":            "registry/os/img@" + d,
	} {
		if got := pinnedRef(in, d); got != want {
			t.Errorf("pinnedRef(%q) = %q, want %q", in, got, want)
		}
	}
}
