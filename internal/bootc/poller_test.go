package bootc

import "testing"

func TestReturnedFrom(t *testing.T) {
	desired := &DesiredImage{ImageRef: "reg/os:new", Digest: "sha256:new"}
	applied := &Attempt{Digest: "sha256:new", PreviousDigest: "sha256:old"}
	restaged := &Attempt{Digest: "sha256:new", PreviousDigest: "sha256:old", Restaged: true}
	cases := []struct {
		name   string
		status *Status
		last   *Attempt
		want   returnKind
	}{
		{"greenboot rollback keeps the rejected image on disk", &Status{Digest: "sha256:old", RollbackDigest: "sha256:new"}, applied, returnRolledBack},
		{"interrupted staging leaves no trace of the new image", &Status{Digest: "sha256:old", RollbackDigest: "sha256:older"}, applied, returnLostStaging},
		{"interrupted staging on a single-deployment node", &Status{Digest: "sha256:old"}, applied, returnLostStaging},
		{"second loss parks", &Status{Digest: "sha256:old", RollbackDigest: "sha256:older"}, restaged, returnLostTwice},
		{"rollback after a re-switch is still a rollback", &Status{Digest: "sha256:old", RollbackDigest: "sha256:new"}, restaged, returnRolledBack},
		{"third image is drift", &Status{Digest: "sha256:other", RollbackDigest: "sha256:old"}, applied, returnDrift},
	}
	for _, c := range cases {
		if got := returnedFrom(c.status, c.last, desired); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}
