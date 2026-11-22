package store

import (
	"os"
	"path/filepath"
	"testing"
)

// A seed-written id ends with a newline (cloud-init write_files); Magnetar
// rejects anything but a bare UUID.
func TestNodeIdGetTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nodeid"), []byte("f210cdd5-ce6a-561e-a06a-29929fc319bd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewNodeIdFSStore(dir, "nodeid")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Get()
	if err != nil {
		t.Fatal(err)
	}
	if id.Value != "f210cdd5-ce6a-561e-a06a-29929fc319bd" {
		t.Fatalf("Get = %q", id.Value)
	}
}
