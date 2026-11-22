package services

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	natsgo "github.com/nats-io/nats.go"

	"github.com/c12s/star/internal/domain"
)

const testNatsAddress = "localhost:4222"

func testNatsConn(t *testing.T) *natsgo.Conn {
	t.Helper()
	// These tests speak the real subjects (registration, heartbeat). Against
	// a live control plane they would enrol a fake node in the real registry,
	// so they only run when explicitly opted in.
	if os.Getenv("C12S_NATS_TESTS") != "1" {
		t.Skip("set C12S_NATS_TESTS=1 to run against a live NATS/control plane")
	}
	conn, err := natsgo.Connect("nats://"+testNatsAddress, natsgo.Timeout(time.Second))
	if err != nil {
		t.Skipf("no NATS server on %s: %v", testNatsAddress, err)
	}
	t.Cleanup(conn.Close)
	return conn
}

// fakeNodeIdStore serves a fixed id, like a node that already enrolled.
type fakeNodeIdStore struct {
	id string
}

func (f fakeNodeIdStore) Get() (*domain.NodeId, error) {
	if f.id == "" {
		return nil, errors.New("no node id")
	}
	return &domain.NodeId{Value: f.id}, nil
}

func (f fakeNodeIdStore) Put(domain.NodeId) error   { return nil }
func (f fakeNodeIdStore) Delete() error             { return nil }
func (f fakeNodeIdStore) PutClusterId(string) error { return nil }

// TestRegisterAssertsStoredId proves the agent re-asserts the id it already
// has, so a control-plane reset cannot duplicate the node under a new UUID.
func TestRegisterAssertsStoredId(t *testing.T) {
	conn := testNatsConn(t)

	const stored = "11111111-1111-4111-8111-111111111111"
	requests := make(chan *magnetarapi.RegistrationReq, 1)
	sub, err := conn.Subscribe(magnetarapi.RegistrationSubject, func(msg *natsgo.Msg) {
		req := &magnetarapi.RegistrationReq{}
		if err := req.Unmarshal(msg.Data); err != nil {
			t.Errorf("unmarshal registration request: %v", err)
			return
		}
		requests <- req
		resp, _ := (&magnetarapi.RegistrationResp{NodeId: req.NodeId}).Marshal()
		_ = msg.Respond(resp)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	client, err := magnetarapi.NewRegistrationAsyncClient(testNatsAddress)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewRegistrationService(client, fakeNodeIdStore{id: stored})

	if err := svc.Register(3, 2*time.Second, "10.0.0.9", []string{"config", "gossip"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-requests:
		if req.NodeId != stored {
			t.Fatalf("registration did not assert the stored id: got %q, want %q", req.NodeId, stored)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no registration request observed")
	}
}

// TestHeartbeatServiceDetectsUnknownNode proves the loop calls the unknown
// handler when Magnetar reports that it has no record for the node.
func TestHeartbeatServiceDetectsUnknownNode(t *testing.T) {
	conn := testNatsConn(t)

	sub, err := conn.Subscribe(magnetarapi.NodeHeartbeatSubject, func(msg *natsgo.Msg) {
		resp, _ := (&magnetarapi.NodeHeartbeatResp{Found: false}).Marshal()
		_ = msg.Respond(resp)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	client, err := magnetarapi.NewNodeHeartbeatAsyncClient(testNatsAddress)
	if err != nil {
		t.Fatal(err)
	}
	unknown := make(chan struct{}, 1)
	svc := NewHeartbeatService(client, "11111111-1111-4111-8111-111111111111", 50*time.Millisecond)
	svc.SetUnknownNodeHandler(func() { unknown <- struct{}{} })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Run(ctx)

	select {
	case <-unknown:
	default:
		t.Fatal("unknown-node handler was not called")
	}
}

// TestHeartbeatServiceKeepsBeating proves a known node keeps running (no
// unknown handler, loop stays alive until the context is cancelled).
func TestHeartbeatServiceKeepsBeating(t *testing.T) {
	conn := testNatsConn(t)

	beats := make(chan struct{}, 8)
	sub, err := conn.Subscribe(magnetarapi.NodeHeartbeatSubject, func(msg *natsgo.Msg) {
		resp, _ := (&magnetarapi.NodeHeartbeatResp{Found: true}).Marshal()
		_ = msg.Respond(resp)
		beats <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	client, err := magnetarapi.NewNodeHeartbeatAsyncClient(testNatsAddress)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewHeartbeatService(client, "11111111-1111-4111-8111-111111111111", 50*time.Millisecond)
	svc.SetUnknownNodeHandler(func() { t.Error("known node must not trigger re-registration") })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	svc.Run(ctx)

	if len(beats) < 2 {
		t.Fatalf("expected repeated heartbeats, got %d", len(beats))
	}
}
