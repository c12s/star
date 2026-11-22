package bootc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type DesiredImage struct {
	ImageRef     string
	Digest       string
	RebootPolicy string
}

type DesiredStore struct {
	path    string
	mu      sync.Mutex
	desired *DesiredImage
}

// NewDesiredStore loads the desired image persisted in stateDir (if any), so
// desired state survives the reboot that bootc switch performs and the
// poller can detect a greenboot rollback after the node comes back.
func NewDesiredStore(stateDir string) *DesiredStore {
	s := &DesiredStore{
		path: filepath.Join(stateDir, "os_desired.json"),
	}
	data, err := os.ReadFile(s.path)
	if err == nil {
		desired := &DesiredImage{}
		if err := json.Unmarshal(data, desired); err == nil {
			s.desired = desired
		}
	}
	return s
}

func (s *DesiredStore) Set(desired DesiredImage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired = &desired
	if s.path != "" {
		if data, err := json.Marshal(desired); err == nil {
			_ = os.WriteFile(s.path, data, 0600)
		}
	}
}

func (s *DesiredStore) Get() *DesiredImage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.desired
}

func (s *DesiredStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.desired = nil
	if s.path != "" {
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return
		}
	}
}

type Attempt struct {
	Image          string `json:"image"`
	Digest         string `json:"digest"`
	PreviousDigest string `json:"previousDigest,omitempty"`
	Result         string `json:"result"`
	Timestamp      int64  `json:"timestamp"`
	// BootID is the kernel boot id at the time the switch was staged. After
	// a reboot it no longer matches, which is what lets the poller tell
	// "deployment staged, reboot still pending" apart from "we rebooted and
	// came back on another image" (greenboot rollback / drift).
	BootID string `json:"bootId,omitempty"`
	// Restaged marks a second switch after the first staged deployment was
	// lost before it reached disk; a second loss parks the digest.
	Restaged bool `json:"restaged,omitempty"`
}

type AttemptStore struct {
	path string
	mu   sync.Mutex
}

func NewAttemptStore(stateDir string) *AttemptStore {
	return &AttemptStore{
		path: filepath.Join(stateDir, "os_attempt.json"),
	}
}

func (s *AttemptStore) Record(attempt Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func (s *AttemptStore) Last() *Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	attempt := &Attempt{}
	if err := json.Unmarshal(data, attempt); err != nil {
		return nil
	}
	return attempt
}

func (s *AttemptStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
