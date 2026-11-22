package bootc

import (
	"fmt"
	"log"

	agent_queue "github.com/c12s/agent_queue/pkg/api"
	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/golang/protobuf/proto"
	natsgo "github.com/nats-io/nats.go"
)

func OSUpdateSubject(nodeId string) string {
	return fmt.Sprintf("%s.os_update", nodeId)
}

type Handler struct {
	conn     *natsgo.Conn
	nodeId   string
	desired  *DesiredStore
	attempts *AttemptStore
	sub      *natsgo.Subscription
}

func NewHandler(conn *natsgo.Conn, nodeId string, desired *DesiredStore, attempts *AttemptStore) *Handler {
	return &Handler{
		conn:     conn,
		nodeId:   nodeId,
		desired:  desired,
		attempts: attempts,
	}
}

func (h *Handler) Serve() error {
	sub, err := h.conn.Subscribe(OSUpdateSubject(h.nodeId), func(msg *natsgo.Msg) {
		cmd := &agent_queue.OSUpdateCmd{}
		if err := proto.Unmarshal(msg.Data, cmd); err != nil {
			log.Printf("[os_update] invalid command: %s", err)
			return
		}
		if cmd.Rollback {
			log.Println("[os_update] rollback command received")
			if len(msg.Reply) > 0 {
				ack, err := proto.Marshal(&agent_queue.OSUpdateAck{})
				if err == nil {
					_ = h.conn.Publish(msg.Reply, ack)
				}
			}
			h.desired.Clear()
			go func() {
				if err := Rollback(); err != nil {
					log.Printf("[os_update] rollback failed: %s", err)
				}
			}()
			return
		}
		if cmd.Digest == "" || cmd.ImageRef == "" {
			log.Println("[os_update] command missing image_ref or digest, ignoring")
			return
		}
		// An explicit new command for a digest that was parked after a
		// greenboot rollback is the operator saying "retry this image":
		// clear the park so the poller attempts it again. The in-flight
		// guard in the poller still prevents duplicate switches.
		if h.attempts != nil {
			if last := h.attempts.Last(); last != nil && last.Digest == cmd.Digest && last.Result == magnetarapi.OsAttemptRolledBack {
				log.Println("[os_update] operator re-declared a rolled-back digest, clearing parked attempt")
				_ = h.attempts.Clear()
			}
		}
		log.Printf("[os_update] desired image set: %s@%s (reboot policy: %s)", cmd.ImageRef, cmd.Digest, cmd.RebootPolicy)
		h.desired.Set(DesiredImage{
			ImageRef:     cmd.ImageRef,
			Digest:       cmd.Digest,
			RebootPolicy: cmd.RebootPolicy,
		})
		if len(msg.Reply) > 0 {
			ack, err := proto.Marshal(&agent_queue.OSUpdateAck{})
			if err == nil {
				_ = h.conn.Publish(msg.Reply, ack)
			}
		}
	})
	if err != nil {
		return err
	}
	h.sub = sub
	return nil
}

func (h *Handler) GracefulStop() {
	if h.sub != nil {
		_ = h.sub.Unsubscribe()
	}
}
