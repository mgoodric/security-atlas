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

// strictReadErrors counts EnabledStrict reads that failed on a flag-store
// error (#2575). Each is a background sweep that skipped a tenant, or a
// request answered with an error, rather than one that guessed the switch
// from its seed default.
var strictReadErrors struct {
	once    sync.Once
	counter metric.Int64Counter
	n       atomic.Uint64
}

func countStrictReadError(ctx context.Context, key string) {
	strictReadErrors.n.Add(1)
	strictReadErrors.once.Do(func() {
		c, err := otel.Meter(meterName).Int64Counter("atlas.module_switch.strict_read_errors",
			metric.WithDescription("Module switch reads that failed closed because the flag store could not be read"))
		if err == nil {
			strictReadErrors.counter = c
		}
	})
	if c := strictReadErrors.counter; c != nil {
		c.Add(ctx, 1, metric.WithAttributes(attribute.String("flag_key", key)))
	}
}

// StrictReadErrors returns how many EnabledStrict reads have failed on a
// flag-store error since the process started.
func StrictReadErrors() uint64 { return strictReadErrors.n.Load() }
