package webhookrecv

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Server timeout defaults satisfying gosec G112 (Slowloris) and bounding a slow
// client. These are the values the hris (slice 573) and pagerduty (slice 540)
// receivers already used; factored here so a fourth connector inherits them.
const (
	defaultReadHeaderTimeout = 10 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
)

// NewServer wraps handler in a bounded http.Server mounted at path. The timeouts
// satisfy gosec G112 (Slowloris) and bound a slow client; the receiver is a
// long-lived process the connector's `run --profile=subscribe` owns.
func NewServer(addr, path string, handler http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
	}
}

// Serve runs srv until ctx is cancelled, then drains it with a bounded graceful
// shutdown. It blocks; the connector's run loop calls it. A returned
// http.ErrServerClosed (the normal shutdown path) is squashed to nil.
func Serve(ctx context.Context, srv *http.Server) error {
	errc := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errc <- err
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		return <-errc
	}
}
