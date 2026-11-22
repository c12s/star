package bootc

import "testing"

func TestHealthcheckFinished(t *testing.T) {
	for state, want := range map[string]bool{
		"active":       true,
		"failed":       true,
		"inactive":     false,
		"activating":   false,
		"deactivating": false,
		"":             false,
	} {
		if got := healthcheckFinished(state); got != want {
			t.Errorf("healthcheckFinished(%q) = %v, want %v", state, got, want)
		}
	}
}
