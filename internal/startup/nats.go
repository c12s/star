package startup

import (
	"fmt"

	magnetarapi "github.com/c12s/magnetar/pkg/api"
	"github.com/nats-io/nats.go"
)

func NewNatsConn(address string) (*nats.Conn, error) {
	return nats.Connect(fmt.Sprintf("nats://%s", address), magnetarapi.NatsOptions()...)
}
