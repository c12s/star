package services

import "testing"

// Whole-number division used to truncate: 1.9 GB of RAM became 1, 0.95 became 0.
func TestBytesToGB(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  float64
	}{
		{1_900_000_000, 1.9},
		{950_000_000, 0.95},
		{0, 0},
	}
	for _, c := range cases {
		if got := bytesToGB(c.bytes); got != c.want {
			t.Errorf("bytesToGB(%d) = %v, want %v", c.bytes, got, c.want)
		}
	}
}
