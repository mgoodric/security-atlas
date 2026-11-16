package authzmw

import (
	"bytes"
	"io"
	"net/http"
)

// ChainResourceAttrs runs each ResourceAttrsFunc in turn and merges what
// they return; a later func's key wins. The first error stops the chain.
// nil funcs are skipped, so a hydrator that is not wired (no DB) drops out.
func ChainResourceAttrs(fns ...ResourceAttrsFunc) ResourceAttrsFunc {
	return func(r *http.Request) (map[string]any, error) {
		var out map[string]any
		for _, fn := range fns {
			if fn == nil {
				continue
			}
			extra, err := fn(r)
			if err != nil {
				return nil, err
			}
			for k, v := range extra {
				if out == nil {
					out = map[string]any{}
				}
				out[k] = v
			}
		}
		return out, nil
	}
}

// PeekBody reads up to limit bytes of r.Body for a ResourceAttrsFunc and
// puts them back in front of the unread remainder, so the handler decodes
// the same bytes. A body longer than limit is still served in full; the
// hydrator just sees its prefix.
func PeekBody(r *http.Request, limit int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	peek, err := io.ReadAll(io.LimitReader(r.Body, limit))
	r.Body = replayedBody{io.MultiReader(bytes.NewReader(peek), r.Body), r.Body}
	return peek, err
}

// replayedBody reads from the replayed body and closes the original one.
type replayedBody struct {
	io.Reader
	io.Closer
}
