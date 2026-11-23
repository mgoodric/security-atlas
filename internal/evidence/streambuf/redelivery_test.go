package streambuf

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func TestRedeliveryDelayFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		delivered uint64
		want      time.Duration
	}{
		{0, 2 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{8, 256 * time.Second},
		{9, 5 * time.Minute},
		{1 << 40, 5 * time.Minute},
	}
	for _, c := range cases {
		if got := redeliveryDelayFor(c.delivered); got != c.want {
			t.Errorf("redeliveryDelayFor(%d) = %s, want %s", c.delivered, got, c.want)
		}
	}
}

// metaMsg is a jetstream.Msg whose only live method is Metadata.
type metaMsg struct {
	jetstream.Msg
	meta *jetstream.MsgMetadata
	err  error
}

func (m metaMsg) Metadata() (*jetstream.MsgMetadata, error) { return m.meta, m.err }

func TestRedeliveryDelay(t *testing.T) {
	t.Parallel()
	if got := RedeliveryDelay(metaMsg{meta: &jetstream.MsgMetadata{NumDelivered: 4}}); got != 16*time.Second {
		t.Errorf("4th delivery: got %s, want 16s", got)
	}
	if got := RedeliveryDelay(metaMsg{err: errors.New("no metadata")}); got != RedeliveryBaseDelay {
		t.Errorf("no metadata: got %s, want %s", got, RedeliveryBaseDelay)
	}
}
