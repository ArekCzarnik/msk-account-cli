package kafka

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"strings"
	"time"

	"github.com/IBM/sarama"
	"github.com/czarnik/msk-account-cli/internal/logging"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TestProduce attempts to produce a single message to the given topic.
func TestProduce(ctx context.Context, a AuthConfig, topic string, payload []byte) error {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.test.produce",
		trace.WithAttributes(
			attribute.String("topic", topic),
			attribute.String("brokers", strings.Join(a.Brokers, ",")),
			attribute.String("scram", a.SCRAMMechanism),
		))
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.test.produce.start", "topic", topic)
	}
	cfg, err := newSaramaConfig(a)
	if err != nil {
		return err
	}
	cfg.Producer.Return.Successes = true
	cfg.Producer.Timeout = 10 * time.Second
	prod, err := sarama.NewSyncProducer(a.Brokers, cfg)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.test.produce.connect_error", "error", err)
		}
		return err
	}
	defer func() { _ = prod.Close() }()
	msg := &sarama.ProducerMessage{Topic: topic, Value: sarama.ByteEncoder(payload)}
	_, _, err = prod.SendMessage(msg)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.test.produce.error", "error", err)
		}
		return err
	}
	if logging.L != nil {
		logging.L.Info("kafka.test.produce.ok")
	}
	return nil
}

// TestDescribeTopic verifies topic metadata can be described.
func TestDescribeTopic(ctx context.Context, a AuthConfig, topic string) error {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.test.describe_topic",
		trace.WithAttributes(
			attribute.String("topic", topic),
			attribute.String("brokers", strings.Join(a.Brokers, ",")),
		))
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.test.describe_topic.start", "topic", topic)
	}
	cfg, err := newSaramaConfig(a)
	if err != nil {
		return err
	}
	client, err := sarama.NewClient(a.Brokers, cfg)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.test.describe_topic.connect_error", "error", err)
		}
		return err
	}
	defer client.Close()
	// Trigger metadata request for topic
	parts, err := client.Partitions(topic)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.test.describe_topic.error", "error", err)
		}
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("topic %s has no partitions (unexpected)", topic)
	}
	if logging.L != nil {
		logging.L.Info("kafka.test.describe_topic.ok", "partitions", len(parts))
	}
	return nil
}

// TestConsume attempts to join a consumer group and start a short poll on the topic.
// Note: This validates group-level permission and basic fetch; if the topic has no data,
// the test may not exercise READ on the topic fully but will still validate group access.
func TestConsume(ctx context.Context, a AuthConfig, topic, groupID string) error {
	tctx, span := otel.Tracer("github.com/czarnik/msk-account-cli/kafka").Start(ctx, "kafka.test.consume",
		trace.WithAttributes(
			attribute.String("topic", topic),
			attribute.String("group.id", groupID),
			attribute.String("brokers", strings.Join(a.Brokers, ",")),
		))
	defer span.End()
	_ = tctx
	if logging.L != nil {
		logging.L.Info("kafka.test.consume.start", "topic", topic, "group_id", groupID)
	}
	cfg, err := newSaramaConfig(a)
	if err != nil {
		return err
	}
	cfg.Consumer.Group.Session.Timeout = 10 * time.Second
	cfg.Consumer.Group.Heartbeat.Interval = 3 * time.Second
	cg, err := sarama.NewConsumerGroup(a.Brokers, groupID, cfg)
	if err != nil {
		if logging.L != nil {
			logging.L.Error("kafka.test.consume.connect_error", "error", err)
		}
		return err
	}
	defer func() { _ = cg.Close() }()

	// Minimal handler that returns quickly; joining the group will already validate access.
	handler := &testConsumerHandler{done: make(chan struct{})}
	// Run a single iteration with the provided context
	topics := []string{topic}
	errCh := make(chan error, 1)
	go func() {
		errCh <- cg.Consume(ctx, topics, handler)
	}()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			if logging.L != nil {
				logging.L.Error("kafka.test.consume.error", "error", err)
			}
			return err
		}
	case <-handler.done:
		// handler signaled setup/teardown; ok
	case <-ctx.Done():
		// timeout/cancel; consider ok if no explicit error occurred
		if logging.L != nil {
			logging.L.Info("kafka.test.consume.timeout")
		}
	}
	if logging.L != nil {
		logging.L.Info("kafka.test.consume.ok")
	}
	return nil
}

type testConsumerHandler struct{ done chan struct{} }

func (h *testConsumerHandler) Setup(s sarama.ConsumerGroupSession) error   { close(h.done); return nil }
func (h *testConsumerHandler) Cleanup(s sarama.ConsumerGroupSession) error { return nil }
func (h *testConsumerHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	// Drain quickly without waiting for data; we only test join/permission.
	select {
	case <-sess.Context().Done():
		return nil
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}

// Build sarama config for clients (TLS + SCRAM) consistent with admin.
func newSaramaConfig(a AuthConfig) (*sarama.Config, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_5_0_0
	cfg.Net.TLS.Enable = true
	cfg.Net.SASL.Enable = true
	cfg.Net.SASL.User = a.SASLUsername
	cfg.Net.SASL.Password = a.SASLPassword
	cfg.Net.SASL.Handshake = true
	cfg.Net.DialTimeout = 10 * time.Second
	cfg.Net.ReadTimeout = 30 * time.Second
	cfg.Net.WriteTimeout = 30 * time.Second

	mech := strings.ToLower(strings.TrimSpace(a.SCRAMMechanism))
	if mech == "" {
		mech = "sha512"
	}
	if mech == "sha256" {
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &xdgSCRAMClient{hashGeneratorFcn: sha256.New}
		}
	} else {
		cfg.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		cfg.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &xdgSCRAMClient{hashGeneratorFcn: sha512.New}
		}
	}
	return cfg, nil
}
