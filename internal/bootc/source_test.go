package bootc

import (
	"context"
	"errors"
	"testing"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
)

type fakeSource struct {
	resp *magnetarapi.DesiredOsImageGetResp
	err  error
}

func (f *fakeSource) GetDesiredOsImage(context.Context, string) (*magnetarapi.DesiredOsImageGetResp, error) {
	return f.resp, f.err
}

func TestPullDesired(t *testing.T) {
	a := DesiredImage{ImageRef: "reg/os:a", Digest: "sha256:aa"}
	b := DesiredImage{ImageRef: "reg/os:b", Digest: "sha256:bb", RebootPolicy: "immediate"}
	declared := func(img DesiredImage) *magnetarapi.DesiredOsImageGetResp {
		return &magnetarapi.DesiredOsImageGetResp{Image: &magnetarapi.DesiredOsImage{
			ImageRef: img.ImageRef, Digest: img.Digest, RebootPolicy: img.RebootPolicy,
		}}
	}
	cases := []struct {
		name   string
		local  *DesiredImage
		source *fakeSource
		want   *DesiredImage
	}{
		{"control plane declares new image", &a, &fakeSource{resp: declared(b)}, &b},
		{"fresh node adopts declared image", nil, &fakeSource{resp: declared(a)}, &a},
		{"same image is a no-op", &a, &fakeSource{resp: declared(a)}, &a},
		{"nothing declared clears local", &a, &fakeSource{resp: &magnetarapi.DesiredOsImageGetResp{}}, nil},
		{"no answer keeps local", &a, &fakeSource{err: errors.New("nats: no responders available for request")}, &a},
		{"unknown node keeps local", &a, &fakeSource{resp: &magnetarapi.DesiredOsImageGetResp{NotFound: true}}, &a},
	}
	for _, c := range cases {
		p := NewPoller(0, t.TempDir(), nil)
		if c.local != nil {
			p.desired.Set(*c.local)
		}
		p.SetDesiredSource(c.source, "n1")
		p.pullDesired()
		got := p.desired.Get()
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: desired = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestPullDesiredPersists(t *testing.T) {
	dir := t.TempDir()
	p := NewPoller(0, dir, nil)
	p.SetDesiredSource(&fakeSource{resp: &magnetarapi.DesiredOsImageGetResp{Image: &magnetarapi.DesiredOsImage{ImageRef: "reg/os:a", Digest: "sha256:aa"}}}, "n1")
	p.pullDesired()
	// survives the reboot a switch performs, and a later control-plane outage
	if got := NewDesiredStore(dir).Get(); got == nil || got.Digest != "sha256:aa" {
		t.Errorf("pulled image not persisted: %+v", got)
	}
}

func TestPullDesiredParkedDigest(t *testing.T) {
	dir := t.TempDir()
	p := NewPoller(0, dir, nil)
	parked := Attempt{Image: "reg/os:b", Digest: "sha256:bb", Result: magnetarapi.OsAttemptRolledBack}
	declaredB := &fakeSource{resp: &magnetarapi.DesiredOsImageGetResp{Image: &magnetarapi.DesiredOsImage{ImageRef: "reg/os:b", Digest: "sha256:bb"}}}

	// the parked digest is still the declared one: keep the park
	p.desired.Set(DesiredImage{ImageRef: "reg/os:b", Digest: "sha256:bb"})
	_ = p.attempts.Record(parked)
	p.SetDesiredSource(declaredB, "n1")
	p.pullDesired()
	if last := p.attempts.Last(); last == nil || last.Result != magnetarapi.OsAttemptRolledBack {
		t.Fatalf("unchanged declaration lifted the park: %+v", last)
	}

	// operator moved to another image and then back to the parked one: retry
	p.desired.Set(DesiredImage{ImageRef: "reg/os:a", Digest: "sha256:aa"})
	p.pullDesired()
	if last := p.attempts.Last(); last != nil {
		t.Errorf("re-declared digest still parked: %+v", last)
	}
}
