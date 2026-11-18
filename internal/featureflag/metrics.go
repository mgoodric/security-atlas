package featureflag

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const meterName = "github.com/mgoodric/security-atlas/internal/featureflag"

// moduleStateErrors counts the requests ModuleGate answered 503 because a
// module switch could not be read (#2339). The OTel counter is created on
// first use, after main has wired the MeterProvider; the in-process count
// lets tests assert it without a collector.
var moduleStateErrors struct {
	once    sync.Once
	counter metric.Int64Counter
	n       atomic.Uint64
}

func countModuleStateError(ctx context.Context, module string) {
	moduleStateErrors.n.Add(1)
	moduleStateErrors.once.Do(func() {
		c, err := otel.Meter(meterName).Int64Counter("atlas.module_gate.state_errors",
			metric.WithDescription("Requests the module gate answered 503 because a module switch could not be read"))
		if err == nil {
			moduleStateErrors.counter = c
		}
	})
	if c := moduleStateErrors.counter; c != nil {
		c.Add(ctx, 1, metric.WithAttributes(attribute.String("module", module)))
	}
}

// ModuleStateErrors returns how many requests ModuleGate has answered 503
// for a failed module-state read since the process started.
func ModuleStateErrors() uint64 { return moduleStateErrors.n.Load() }
