package bootc

import "testing"

func TestResolveImageRef(t *testing.T) {
	cases := []struct {
		ref, registry, want string
		wantErr             bool
	}{
		{"os/edge-base:v4.1", "192.168.122.1:5001", "192.168.122.1:5001/os/edge-base:v4.1", false},
		{"os/edge-base:v4.1", "192.168.122.1:5001/", "192.168.122.1:5001/os/edge-base:v4.1", false},
		{"edge-base:v4.1", "registry.lab", "registry.lab/edge-base:v4.1", false},
		// an explicit host is an override and wins
		{"other.example.com/os/edge-base:v4.1", "192.168.122.1:5001", "other.example.com/os/edge-base:v4.1", false},
		{"localhost:5001/os/edge-base:v4.1", "192.168.122.1:5001", "localhost:5001/os/edge-base:v4.1", false},
		// never fall back to docker.io
		{"os/edge-base:v4.1", "", "", true},
	}
	for _, c := range cases {
		got, err := ResolveImageRef(c.ref, c.registry)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("ResolveImageRef(%q, %q) = %q, %v; want %q (err %v)", c.ref, c.registry, got, err, c.want, c.wantErr)
		}
	}
}
