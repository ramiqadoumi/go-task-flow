package kafka

import segkafka "github.com/segmentio/kafka-go"

// HeaderCarrier adapts a Kafka message's []Header slice to the
// OpenTelemetry propagation.TextMapCarrier interface, enabling trace context
// to be injected into outgoing messages and extracted from incoming ones.
type HeaderCarrier []segkafka.Header

// Get returns the value for the first header matching key, or "".
func (c HeaderCarrier) Get(key string) string {
	for _, h := range c {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Set writes key/value, replacing any existing header with the same key.
func (c *HeaderCarrier) Set(key, value string) {
	filtered := (*c)[:0]
	for _, h := range *c {
		if h.Key != key {
			filtered = append(filtered, h)
		}
	}
	*c = append(filtered, segkafka.Header{Key: key, Value: []byte(value)})
}

// Keys returns all header keys present in the carrier.
func (c HeaderCarrier) Keys() []string {
	keys := make([]string, len(c))
	for i, h := range c {
		keys[i] = h.Key
	}
	return keys
}
