package taskcenter

import "context"

// Message contains identifiers only. Execution parameters always come from the
// database; schema version and task dispatch version have distinct purposes.
type Message struct {
	SchemaVersion   int    `json:"schema_version"`
	EventID         string `json:"event_id"`
	TaskID          string `json:"task_id"`
	Kind            string `json:"kind"`
	DispatchVersion int64  `json:"dispatch_version"`
}

type PublisherBroker interface {
	Publish(context.Context, OutboxEvent) error
}
type ConsumerBroker interface {
	Subscribe(context.Context, string) (Subscription, error)
}

// Stop cancels new deliveries while preserving the original channel for ACK
// during drain. Close cancels execution and requeues unacknowledged deliveries.
type Subscription interface {
	Deliveries() <-chan Delivery
	Done() <-chan error
	Context() context.Context
	Stop() error
	Close() error
}

type Delivery struct {
	Body        []byte
	MessageID   string
	Headers     map[string]any
	RoutingKey  string
	Redelivered bool
	AckFunc     func() error
	NackFunc    func(bool) error
}

func (d Delivery) Ack() error {
	if d.AckFunc == nil {
		return ErrInvalid
	}
	return d.AckFunc()
}
func (d Delivery) Nack(requeue bool) error {
	if d.NackFunc == nil {
		return ErrInvalid
	}
	return d.NackFunc(requeue)
}
