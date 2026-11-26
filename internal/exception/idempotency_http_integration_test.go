//go:build integration

// #2618 API tests for Idempotency-Key on POST /v1/exceptions, through the
// full HTTP stack against real Postgres under RLS:
//
//   - a retry with the same key and body replays the first exception
//     (same id, Idempotent-Replayed: true) and files nothing new;
//   - the same key with a different body is refused 422;
//   - the same key from a different person is a different request;
//   - concurrent sends of one key file one exception;
//   - a request without a key keeps filing a new exception each time.

package exception_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// postWithKey sends POST /v1/exceptions with an Idempotency-Key and
// returns the status, the Idempotent-Replayed header and the body.
func (a *exceptionAPI) postWithKey(bearer, key, body string) (int, string, []byte) {
	a.t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.ts.URL+"/v1/exceptions", strings.NewReader(body))
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Errorf("POST /v1/exceptions: %v", err)
		return 0, "", nil
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Idempotent-Replayed"), out
}

func (a *exceptionAPI) createBody(justification string) string {
	return fmt.Sprintf(`{"control_id":%q,"justification":%q,"expires_at":%q}`,
		a.ctrl, justification, time.Now().UTC().Add(30*24*time.Hour).Truncate(time.Hour).Format(time.RFC3339))
}

func (a *exceptionAPI) countFor(bearer string) int {
	a.t.Helper()
	code, raw := a.do(http.MethodGet, "/v1/exceptions?control_id="+a.ctrl.String(), bearer, "")
	if code != http.StatusOK {
		a.t.Fatalf("list: status %d body=%s", code, raw)
	}
	var out struct {
		Exceptions []json.RawMessage `json:"exceptions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		a.t.Fatalf("decode list: %v body=%s", err, raw)
	}
	return len(out.Exceptions)
}

func TestHTTP_ExceptionCreate_IdempotencyKeyReplays(t *testing.T) {
	a, _ := newExceptionAPI(t)
	requester := a.newPerson()
	key := uuid.New().String()
	body := a.createBody("vendor blocker")
	before := a.countFor(requester)

	code, replayed, raw := a.postWithKey(requester, key, body)
	if code != http.StatusCreated || replayed != "" {
		t.Fatalf("first send: status %d replayed=%q body=%s", code, replayed, raw)
	}
	first := decodeException(t, raw).Exception.ID

	code, replayed, raw = a.postWithKey(requester, key, body)
	if code != http.StatusCreated || replayed != "true" {
		t.Fatalf("retry: status %d replayed=%q body=%s, want 201 replayed", code, replayed, raw)
	}
	if got := decodeException(t, raw).Exception.ID; got != first {
		t.Fatalf("retry returned exception %s, want the first one %s", got, first)
	}
	if n := a.countFor(requester) - before; n != 1 {
		t.Fatalf("filed %d exceptions for one key, want 1", n)
	}

	// The same key with a different body is a client bug, not a retry.
	code, _, raw = a.postWithKey(requester, key, a.createBody("a different reason"))
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("same key, different body: status %d body=%s, want 422", code, raw)
	}

	// Another person's key is their own.
	other := a.newPerson()
	code, replayed, raw = a.postWithKey(other, key, body)
	if code != http.StatusCreated || replayed != "" {
		t.Fatalf("other requester, same key: status %d replayed=%q body=%s, want a new exception", code, replayed, raw)
	}
	if decodeException(t, raw).Exception.ID == first {
		t.Fatal("another requester's key replayed the first requester's exception")
	}
}

func TestHTTP_ExceptionCreate_IdempotencyKeyConcurrent(t *testing.T) {
	a, _ := newExceptionAPI(t)
	requester := a.newPerson()
	key := uuid.New().String()
	body := a.createBody("concurrent retry")
	before := a.countFor(requester)

	const n = 5
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, raw := a.postWithKey(requester, key, body)
			if code != http.StatusCreated {
				t.Errorf("send %d: status %d body=%s", i, code, raw)
				return
			}
			var out exceptionBody
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Errorf("send %d: decode: %v", i, err)
				return
			}
			ids[i] = out.Exception.ID
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if ids[i] != ids[0] {
			t.Fatalf("concurrent sends of one key returned %v, want one id", ids)
		}
	}
	if got := a.countFor(requester) - before; got != 1 {
		t.Fatalf("concurrent sends filed %d exceptions, want 1", got)
	}
}

func TestHTTP_ExceptionCreate_NoKeyFilesEachTime(t *testing.T) {
	a, _ := newExceptionAPI(t)
	requester := a.newPerson()
	body := a.createBody("no key")
	before := a.countFor(requester)
	for range 2 {
		if code, _, raw := a.postWithKey(requester, "", body); code != http.StatusCreated {
			t.Fatalf("send: status %d body=%s", code, raw)
		}
	}
	if got := a.countFor(requester) - before; got != 2 {
		t.Fatalf("two keyless sends filed %d exceptions, want 2", got)
	}
	if code, _, raw := a.postWithKey(requester, "bad key with spaces", body); code != http.StatusBadRequest {
		t.Fatalf("malformed key: status %d body=%s, want 400", code, raw)
	}
}
