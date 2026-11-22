package bootc

import "testing"

// Shape of `bootc status --json` (trimmed): the rollback entry carries the
// previous image, which `image rollback` targets.
const statusJSON = `{"status":{
 "booted":{"image":{"image":{"image":"192.168.122.1:5001/os/edge-base:v4.1","transport":"registry"},
   "version":"v4.1","imageDigest":"sha256:new"}},
 "rollback":{"image":{"image":{"image":"192.168.122.1:5001/os/edge-base:v4.0","transport":"registry"},
   "version":"v4.0","imageDigest":"sha256:old"}}}}`

func TestParseStatusRollback(t *testing.T) {
	st := parseStatus([]byte(statusJSON))
	if st == nil {
		t.Fatal("nil status")
	}
	if st.Digest != "sha256:new" || st.Image != "192.168.122.1:5001/os/edge-base:v4.1" {
		t.Fatalf("booted: %+v", st)
	}
	if st.RollbackDigest != "sha256:old" || st.RollbackImage != "192.168.122.1:5001/os/edge-base:v4.0" {
		t.Fatalf("rollback: %+v", st)
	}
}
