package webhookrecv_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/connectors/shared/webhookrecv"
)

// okVerifier accepts iff the X-Sig header equals "good".
type okVerifier struct{}

func (okVerifier) Verify(_ []byte, h http.Header) error {
	if h.Get("X-Sig") == "good" {
		return nil
	}
	return webhookrecv.ErrBadSignature
}

const skeletonMax int64 = 1 << 10

func skeletonHandler(build webhookrecv.BuildAndPush) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		webhookrecv.Handle(w, r, skeletonMax, okVerifier{}, build)
	}
}

func TestHandle_NonPostIs405(t *testing.T) {
	t.Parallel()
	h := skeletonHandler(func(*http.Request, []byte) int { return http.StatusOK })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/hook", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d; want 405", w.Code)
	}
}

func TestHandle_OversizedIs413(t *testing.T) {
	t.Parallel()
	called := false
	h := skeletonHandler(func(*http.Request, []byte) int { called = true; return http.StatusOK })
	big := bytes.Repeat([]byte("a"), int(skeletonMax)+1)
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader(big))
	req.Header.Set("X-Sig", "good")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want 413", w.Code)
	}
	if called {
		t.Fatal("build invoked on oversized body; must reject before build")
	}
}

func TestHandle_VerifyFailsIs401BeforeBuild(t *testing.T) {
	t.Parallel()
	called := false
	h := skeletonHandler(func(*http.Request, []byte) int { called = true; return http.StatusOK })
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte(`{}`)))
	// No / wrong signature.
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", w.Code)
	}
	if called {
		t.Fatal("build invoked on failed verification; verify-first P0 violated")
	}
}

func TestHandle_VerifiedBodyReachesBuild(t *testing.T) {
	t.Parallel()
	var got []byte
	h := skeletonHandler(func(_ *http.Request, body []byte) int {
		got = body
		return http.StatusOK
	})
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte(`{"x":1}`)))
	req.Header.Set("X-Sig", "good")
	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	if string(got) != `{"x":1}` {
		t.Fatalf("build got body %q; want verified body", got)
	}
}

func TestHandle_BuildStatusIsWritten(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusBadGateway} {
		st := status
		h := skeletonHandler(func(*http.Request, []byte) int { return st })
		req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("X-Sig", "good")
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != st {
			t.Fatalf("build returned %d; handler wrote %d", st, w.Code)
		}
	}
}
