package streambuf

import (
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Redelivery backoff for the evidence-ingest consumers (#3005). Every
// consumer on the stream runs with MaxDeliver: -1, so a message whose
// callee keeps failing redelivers forever. A fixed 2s NAK delay turns an
// error that is misclassified as transient into a hot loop. The delay
// instead doubles per delivery from RedeliveryBaseDelay up to
// RedeliveryMaxDelay, so a real transient blip still retries within
// seconds while a stuck message costs one attempt every few minutes.
const (
	RedeliveryBaseDelay = 2 * time.Second
	RedeliveryMaxDelay  = 5 * time.Minute
)

// RedeliveryDelay is the NAK delay for msg: RedeliveryBaseDelay on the first
// delivery, doubling per further delivery, capped at RedeliveryMaxDelay.
// When the message carries no metadata (a test double, or a core NATS
// message) it is RedeliveryBaseDelay.
func RedeliveryDelay(msg jetstream.Msg) time.Duration {
	meta, err := msg.Metadata()
	if err != nil || meta == nil {
		return RedeliveryBaseDelay
	}
	return redeliveryDelayFor(meta.NumDelivered)
}

func redeliveryDelayFor(numDelivered uint64) time.Duration {
	d := RedeliveryBaseDelay
	for n := uint64(1); n < numDelivered; n++ {
		d *= 2
		if d >= RedeliveryMaxDelay {
			return RedeliveryMaxDelay
		}
	}
	return d
}
