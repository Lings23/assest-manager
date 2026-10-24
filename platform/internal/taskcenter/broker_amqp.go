package taskcenter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrBrokerClosed    = errors.New("AMQP broker closed")
	ErrPublishReturned = errors.New("AMQP publish returned as unroutable")
	ErrPublishNack     = errors.New("AMQP publish negatively acknowledged")
)

// AMQPBroker deliberately uses amqp091-go v1.11.0 without client auto recovery.
// Publisher reconnects on its next publish; worker supervises new subscriptions.
// Each consumer slot has its own connection/channel, independent of publishing.
type AMQPBroker struct {
	url, exchange, queue string
	gate                 chan struct{}
	mu                   sync.Mutex
	publisher            *amqpPublisherSession
	subscriptions        map[*amqpSubscription]struct{}
	closed               atomic.Bool
	publishConfirmed     atomic.Uint64
	publishReturns       atomic.Uint64
	publishNacks         atomic.Uint64
	confirmFailures      atomic.Uint64
	transportFailures    atomic.Uint64
}

type amqpTransport struct {
	connection *amqp.Connection
	channel    *amqp.Channel
	socket     net.Conn
}

type amqpPublisherSession struct {
	*amqpTransport
	confirms         <-chan amqp.Confirmation
	returns          <-chan amqp.Return
	channelClosed    <-chan *amqp.Error
	connectionClosed <-chan *amqp.Error
}

func NewAMQPBroker(address string) (*AMQPBroker, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "amqp" && parsed.Scheme != "amqps") {
		return nil, fmt.Errorf("invalid RABBITMQ_URL: expected amqp or amqps URL")
	}
	if _, err = amqp.ParseURI(address); err != nil {
		return nil, fmt.Errorf("invalid RABBITMQ_URL")
	}
	return &AMQPBroker{url: address, exchange: "asset.tasks", queue: "task-report-worker", gate: make(chan struct{}, 1), subscriptions: make(map[*amqpSubscription]struct{})}, nil
}

// openTransport bounds both the TCP/TLS/AMQP handshake and topology RPCs. A
// cancellation closes the actual socket rather than waiting for AMQP Close RPC.
func (b *AMQPBroker) openTransport(ctx context.Context, setup func(*amqp.Channel) error) (*amqpTransport, error) {
	var socket net.Conn
	var stopCancel func() bool
	connection, err := amqp.DialConfig(b.url, amqp.Config{Heartbeat: 10 * time.Second, Locale: "en_US", Dial: func(network, address string) (net.Conn, error) {
		var dialErr error
		socket, dialErr = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, address)
		if dialErr != nil {
			return nil, dialErr
		}
		deadline := time.Now().Add(10 * time.Second)
		if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
			deadline = requested
		}
		_ = socket.SetDeadline(deadline)
		stopCancel = context.AfterFunc(ctx, func() { _ = socket.Close() })
		return socket, nil
	}})
	if stopCancel != nil {
		defer stopCancel()
	}
	if err != nil {
		if socket != nil {
			_ = socket.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The AMQP library may include the credential-bearing URL in errors.
		return nil, fmt.Errorf("AMQP connect failed: %s", redactAMQPError(err))
	}
	// DialConfig clears its handshake deadline. Restore a deadline for channel
	// and topology RPCs before entering the long-lived heartbeat-managed phase.
	setupDeadline := time.Now().Add(10 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(setupDeadline) {
		setupDeadline = requested
	}
	_ = socket.SetDeadline(setupDeadline)
	channel, err := connection.Channel()
	if err == nil {
		err = setup(channel)
	}
	if err != nil || ctx.Err() != nil {
		_ = socket.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	_ = socket.SetDeadline(time.Time{})
	return &amqpTransport{connection: connection, channel: channel, socket: socket}, nil
}

func redactAMQPError(err error) string {
	// Never expose credentials from dial errors in process logs.
	if _, ok := err.(*net.OpError); ok {
		return err.Error()
	}
	if _, ok := err.(*amqp.Error); ok {
		return err.Error()
	}
	return "connection or protocol negotiation failed"
}

func (b *AMQPBroker) declareTopology(channel *amqp.Channel) error {
	for _, exchange := range []string{b.exchange, b.exchange + ".dead"} {
		if err := channel.ExchangeDeclare(exchange, "direct", true, false, false, false, nil); err != nil {
			return err
		}
	}
	if _, err := channel.QueueDeclare(b.queue, true, false, false, false, amqp.Table{"x-dead-letter-exchange": b.exchange + ".dead"}); err != nil {
		return err
	}
	if _, err := channel.QueueDeclare(b.queue+".dead", true, false, false, false, nil); err != nil {
		return err
	}
	if err := channel.QueueBind(b.queue, "task.requested", b.exchange, false, nil); err != nil {
		return err
	}
	for _, key := range []string{"task.dead", "task.requested"} {
		if err := channel.QueueBind(b.queue+".dead", key, b.exchange+".dead", false, nil); err != nil {
			return err
		}
	}
	return nil
}

func (b *AMQPBroker) lockPublisher(ctx context.Context) error {
	select {
	case b.gate <- struct{}{}:
		if b.closed.Load() {
			<-b.gate
			return ErrBrokerClosed
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *AMQPBroker) publisherSession(ctx context.Context) (*amqpPublisherSession, error) {
	b.mu.Lock()
	session := b.publisher
	b.mu.Unlock()
	if session != nil && !session.connection.IsClosed() && !session.channel.IsClosed() {
		return session, nil
	}
	if session != nil {
		_ = session.socket.Close()
	}
	transport, err := b.openTransport(ctx, func(channel *amqp.Channel) error {
		if err := b.declareTopology(channel); err != nil {
			return err
		}
		return channel.Confirm(false)
	})
	if err != nil {
		return nil, err
	}
	session = &amqpPublisherSession{amqpTransport: transport,
		confirms:         transport.channel.NotifyPublish(make(chan amqp.Confirmation, 1)),
		returns:          transport.channel.NotifyReturn(make(chan amqp.Return, 1)),
		channelClosed:    transport.channel.NotifyClose(make(chan *amqp.Error, 1)),
		connectionClosed: transport.connection.NotifyClose(make(chan *amqp.Error, 1))}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		_ = transport.socket.Close()
		return nil, ErrBrokerClosed
	}
	b.publisher = session
	b.mu.Unlock()
	return session, nil
}

func (b *AMQPBroker) Ensure(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := b.lockPublisher(ctx); err != nil {
		return err
	}
	defer func() { <-b.gate }()
	_, err := b.publisherSession(ctx)
	if err != nil {
		b.transportFailures.Add(1)
	}
	return err
}

func (b *AMQPBroker) Ready(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed.Load() || b.publisher == nil || b.publisher.connection.IsClosed() || b.publisher.channel.IsClosed() {
		return ErrBrokerClosed
	}
	return nil
}

func (b *AMQPBroker) Publish(ctx context.Context, event OutboxEvent) error {
	if event.EventID == "" {
		return ErrInvalid
	}
	exchange, key := b.exchange, event.Topic
	switch event.Topic {
	case "task.requested":
	case "task.dead":
		exchange += ".dead"
	default:
		return fmt.Errorf("unsupported outbox topic %q", event.Topic)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := b.lockPublisher(ctx); err != nil {
		return err
	}
	defer func() { <-b.gate }()
	session, err := b.publisherSession(ctx)
	if err != nil {
		b.transportFailures.Add(1)
		return err
	}
	// PublishWithContext does not interrupt a blocked socket write in v1.11.0.
	// Close the socket on cancellation to bound write and confirm waiting alike.
	stop := context.AfterFunc(ctx, func() { _ = session.socket.Close() })
	defer stop()
	sequence := session.channel.GetNextPublishSeqNo()
	err = session.channel.PublishWithContext(ctx, exchange, key, true, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent, ContentType: "application/json", MessageId: event.EventID,
		Headers: amqp.Table{"event_id": event.EventID}, Body: event.Payload, Timestamp: time.Now().UTC(),
	})
	if err == nil {
		err = waitPublishConfirmation(ctx, event.EventID, sequence, session.confirms, session.returns, session.channelClosed, session.connectionClosed)
		if err != nil {
			b.recordConfirmationFailure(err)
		}
	} else {
		b.transportFailures.Add(1)
	}
	if err == nil {
		err = ctx.Err()
		if err != nil {
			b.confirmFailures.Add(1)
		} else {
			b.publishConfirmed.Add(1)
		}
	}
	if err != nil {
		b.mu.Lock()
		if b.publisher == session {
			b.publisher = nil
		}
		b.mu.Unlock()
		_ = session.socket.Close()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

func (b *AMQPBroker) recordConfirmationFailure(err error) {
	switch {
	case errors.Is(err, ErrPublishReturned):
		b.publishReturns.Add(1)
	case errors.Is(err, ErrPublishNack):
		b.publishNacks.Add(1)
	default:
		b.confirmFailures.Add(1)
	}
}

// In pinned amqp091-go v1.11.0, Channel.dispatch delivers basic.return to the
// buffered listener synchronously in the connection reader, before dispatching
// the following basic.ack. After receiving a confirm we must drain that buffer,
// even if select chose the confirm first. Later versions with async listeners
// require a different ordering barrier; this guarantee is covered by real MQ tests.
func waitPublishConfirmation(ctx context.Context, eventID string, sequence uint64, confirms <-chan amqp.Confirmation, returns <-chan amqp.Return, channelClosed, connectionClosed <-chan *amqp.Error) error {
	var returned *amqp.Return
	for {
		select {
		case result, ok := <-returns:
			if !ok {
				return ErrBrokerClosed
			}
			if result.MessageId != eventID {
				return fmt.Errorf("AMQP return event mismatch")
			}
			returned = &result
		case confirmation, ok := <-confirms:
			if !ok {
				return ErrBrokerClosed
			}
			if confirmation.DeliveryTag != sequence {
				return fmt.Errorf("AMQP confirm sequence mismatch")
			}
			select {
			case result, open := <-returns:
				if !open {
					return ErrBrokerClosed
				}
				if result.MessageId != eventID {
					return fmt.Errorf("AMQP return event mismatch")
				}
				returned = &result
			default:
			}
			if returned != nil {
				return fmt.Errorf("%w: %d %s", ErrPublishReturned, returned.ReplyCode, returned.ReplyText)
			}
			if !confirmation.Ack {
				return ErrPublishNack
			}
			return nil
		case err := <-channelClosed:
			return brokerCloseError(err)
		case err := <-connectionClosed:
			return brokerCloseError(err)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func brokerCloseError(err *amqp.Error) error {
	if err == nil {
		return ErrBrokerClosed
	}
	return fmt.Errorf("%w: %s", ErrBrokerClosed, err.Error())
}

func (b *AMQPBroker) Close() error {
	if b.closed.Swap(true) {
		return nil
	}
	b.mu.Lock()
	session := b.publisher
	var subscriptions []*amqpSubscription
	for subscription := range b.subscriptions {
		subscriptions = append(subscriptions, subscription)
	}
	b.mu.Unlock()
	if session != nil {
		_ = session.socket.Close()
	}
	for _, subscription := range subscriptions {
		_ = subscription.Close()
	}
	return nil
}

type amqpSubscription struct {
	*amqpTransport
	broker              *AMQPBroker
	consumerTag         string
	ctx                 context.Context
	cancel              context.CancelFunc
	deliveries          chan Delivery
	done                chan error
	stopOnce, closeOnce sync.Once
	stopErr             error
	stopped             atomic.Bool
	deliveryMu          sync.Mutex
	unacked             int64
}

func (b *AMQPBroker) Subscribe(ctx context.Context, consumerTag string) (Subscription, error) {
	if b.closed.Load() {
		return nil, ErrBrokerClosed
	}
	if consumerTag == "" {
		return nil, ErrInvalid
	}
	var deliveries <-chan amqp.Delivery
	transport, err := b.openTransport(ctx, func(channel *amqp.Channel) error {
		if err := b.declareTopology(channel); err != nil {
			return err
		}
		if err := channel.Qos(1, 0, false); err != nil {
			return err
		}
		var consumeErr error
		deliveries, consumeErr = channel.Consume(b.queue, consumerTag, false, false, false, false, nil)
		return consumeErr
	})
	if err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	subscription := &amqpSubscription{amqpTransport: transport, broker: b, consumerTag: consumerTag, ctx: child, cancel: cancel, deliveries: make(chan Delivery), done: make(chan error, 1)}
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		_ = transport.socket.Close()
		cancel()
		return nil, ErrBrokerClosed
	}
	b.subscriptions[subscription] = struct{}{}
	b.mu.Unlock()
	channelClosed := transport.channel.NotifyClose(make(chan *amqp.Error, 1))
	connectionClosed := transport.connection.NotifyClose(make(chan *amqp.Error, 1))
	consumerCancelled := transport.channel.NotifyCancel(make(chan string, 1))
	go subscription.watch(channelClosed, connectionClosed, consumerCancelled)
	go subscription.forward(deliveries)
	return subscription, nil
}

func (s *amqpSubscription) watch(channelClosed, connectionClosed <-chan *amqp.Error, consumerCancelled <-chan string) {
	var err error
	select {
	case closeErr := <-channelClosed:
		err = brokerCloseError(closeErr)
	case closeErr := <-connectionClosed:
		err = brokerCloseError(closeErr)
	case <-consumerCancelled:
		err = fmt.Errorf("AMQP consumer cancelled by broker")
	case <-s.ctx.Done():
		err = s.ctx.Err()
	}
	_ = s.Close()
	s.done <- err
	close(s.done)
}

func (s *amqpSubscription) forward(deliveries <-chan amqp.Delivery) {
	defer close(s.deliveries)
	for {
		select {
		case message, ok := <-deliveries:
			if !ok {
				if !s.stopped.Load() {
					_ = s.Close()
				}
				return
			}
			ack, nack, tracked := s.trackDelivery(func() error { return message.Ack(false) }, func(requeue bool) error { return message.Nack(false, requeue) })
			if !tracked {
				return
			}
			delivery := Delivery{Body: message.Body, MessageID: message.MessageId, Headers: map[string]any(message.Headers), RoutingKey: message.RoutingKey, Redelivered: message.Redelivered,
				AckFunc: ack, NackFunc: nack}
			select {
			case s.deliveries <- delivery:
			case <-s.ctx.Done():
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *amqpSubscription) Deliveries() <-chan Delivery { return s.deliveries }
func (s *amqpSubscription) Done() <-chan error          { return s.done }
func (s *amqpSubscription) Context() context.Context    { return s.ctx }

func (s *amqpSubscription) Stop() error {
	s.stopOnce.Do(func() {
		s.stopped.Store(true)
		// Keep the channel for in-flight ACK; only force close on a stalled RPC.
		timeout := time.AfterFunc(5*time.Second, func() { _ = s.Close() })
		s.stopErr = s.channel.Cancel(s.consumerTag, false)
		timeout.Stop()
		if s.stopErr != nil {
			_ = s.Close()
		}
	})
	return s.stopErr
}

func (s *amqpSubscription) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.deliveryMu.Lock()
		s.unacked = 0
		s.deliveryMu.Unlock()
		_ = s.socket.Close()
		s.broker.mu.Lock()
		delete(s.broker.subscriptions, s)
		s.broker.mu.Unlock()
	})
	return nil
}
