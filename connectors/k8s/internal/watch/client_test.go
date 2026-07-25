package watch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rawObj is a tiny Raw shape for the client tests: just a name lifted from
// metadata. It stands in for rbac.RawBinding / workload.RawWorkload.
type rawObj struct {
	Name string
}

func decodeRawObj(raw json.RawMessage) (any, error) {
	var m struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return rawObj{Name: m.Metadata.Name}, nil
}

func decodeRawList(body io.Reader) ([]any, string, error) {
	var lst struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.NewDecoder(body).Decode(&lst); err != nil {
		return nil, "", err
	}
	out := make([]any, 0, len(lst.Items))
	for _, it := range lst.Items {
		out = append(out, rawObj{Name: it.Metadata.Name})
	}
	return out, lst.Metadata.ResourceVersion, nil
}

func newTestSource(t *testing.T, handler http.HandlerFunc) (*HTTPSource, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	src := NewHTTPSource(HTTPSourceConfig{
		HTTP:         srv.Client(),
		BaseURL:      srv.URL,
		Token:        "test-watch-token",
		Path:         "/apis/rbac.authorization.k8s.io/v1/rolebindings",
		ListObjects:  decodeRawList,
		DecodeObject: decodeRawObj,
	})
	return src, srv
}

func TestHTTPSource_ListDecodesObjectsAndRV(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") != "" {
			t.Errorf("List must not set watch; got %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-watch-token" {
			t.Errorf("auth header = %q", got)
		}
		_, _ = io.WriteString(w, `{"metadata":{"resourceVersion":"rv-42"},"items":[{"metadata":{"name":"admins"}},{"metadata":{"name":"readers"}}]}`)
	})
	objs, rv, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if rv != "rv-42" {
		t.Errorf("rv = %q; want rv-42", rv)
	}
	if len(objs) != 2 || objs[0].(rawObj).Name != "admins" {
		t.Errorf("objects = %+v", objs)
	}
}

func TestHTTPSource_ListNon200(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, _, err := src.List(context.Background())
	var ae *APIError
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 APIError; got %v", err)
	}
	_ = ae
}

func TestHTTPSource_WatchStreamsFramesAndBookmark(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("watch") != "true" {
			t.Errorf("watch param = %q; want true", q.Get("watch"))
		}
		if q.Get("allowWatchBookmarks") != "true" {
			t.Errorf("allowWatchBookmarks = %q; want true", q.Get("allowWatchBookmarks"))
		}
		if q.Get("resourceVersion") != "rv-7" {
			t.Errorf("resourceVersion = %q; want rv-7", q.Get("resourceVersion"))
		}
		w.Header().Set("Content-Type", "application/json")
		// Newline-delimited watch envelopes: ADDED, BOOKMARK, then a blank
		// keep-alive line, then DELETED.
		_, _ = io.WriteString(w, `{"type":"ADDED","object":{"metadata":{"name":"admins","resourceVersion":"rv-8"}}}`+"\n")
		_, _ = io.WriteString(w, `{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"rv-9"}}}`+"\n")
		_, _ = io.WriteString(w, "\n")
		_, _ = io.WriteString(w, `{"type":"DELETED","object":{"metadata":{"name":"admins","resourceVersion":"rv-10"}}}`+"\n")
	})
	stream, err := src.Watch(context.Background(), "rv-7")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = stream.Close() }()

	ev1, err := stream.Recv()
	if err != nil || ev1.Type != EventAdded || ev1.ResourceVersion != "rv-8" {
		t.Fatalf("ev1 = %+v err=%v", ev1, err)
	}
	if ev1.Object.(rawObj).Name != "admins" {
		t.Errorf("ev1 object = %+v", ev1.Object)
	}
	ev2, err := stream.Recv()
	if err != nil || ev2.Type != EventBookmark || ev2.ResourceVersion != "rv-9" {
		t.Fatalf("ev2 = %+v err=%v", ev2, err)
	}
	// The blank keep-alive line is skipped; next is DELETED.
	ev3, err := stream.Recv()
	if err != nil || ev3.Type != EventDeleted || ev3.ResourceVersion != "rv-10" {
		t.Fatalf("ev3 = %+v err=%v", ev3, err)
	}
	// Stream end → ErrStreamClosed.
	if _, err := stream.Recv(); err == nil {
		t.Error("want stream-closed after frames exhausted")
	}
}

func TestHTTPSource_Watch410GoneFrame(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		// A Status ERROR frame reporting 410 Gone (resourceVersion too old).
		_, _ = io.WriteString(w, `{"type":"ERROR","object":{"kind":"Status","code":410,"reason":"Expired"}}`+"\n")
	})
	stream, err := src.Watch(context.Background(), "rv-old")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = stream.Close() }()
	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ev.Type != EventError || !ev.ResourceExpired {
		t.Errorf("ev = %+v; want ERROR + ResourceExpired", ev)
	}
}

func TestHTTPSource_WatchNon200(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	})
	_, err := src.Watch(context.Background(), "rv-1")
	if !IsResourceExpired(err) {
		t.Fatalf("want IsResourceExpired(410); got %v", err)
	}
}

func TestHTTPSource_WatchMalformedFrame(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not-json\n")
	})
	stream, err := src.Watch(context.Background(), "rv-1")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Recv(); err == nil || !strings.Contains(err.Error(), "decode frame") {
		t.Errorf("want decode-frame error; got %v", err)
	}
}

func TestHTTPSource_WatchUnknownEventType(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"type":"WEIRD","object":{"metadata":{"resourceVersion":"rv-3"}}}`+"\n")
	})
	stream, err := src.Watch(context.Background(), "")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer func() { _ = stream.Close() }()
	ev, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ev.Type != EventType("WEIRD") || ev.ResourceVersion != "rv-3" {
		t.Errorf("ev = %+v", ev)
	}
}

func TestNewHTTPSource_DefaultsNoTimeout(t *testing.T) {
	t.Parallel()
	src := NewHTTPSource(HTTPSourceConfig{BaseURL: "https://k:6443/"})
	if src.http.Timeout != 0 {
		t.Errorf("watch client must have no timeout (long-lived stream); got %v", src.http.Timeout)
	}
	if src.baseURL != "https://k:6443" {
		t.Errorf("baseURL not trimmed: %q", src.baseURL)
	}
}

func TestHTTPSource_WatchEmptyRVOmitsParam(t *testing.T) {
	t.Parallel()
	src, _ := newTestSource(t, func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["resourceVersion"]; ok {
			t.Error("empty RV must omit the resourceVersion param")
		}
		_, _ = io.WriteString(w, `{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"rv-1"}}}`+"\n")
	})
	stream, err := src.Watch(context.Background(), "")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	_ = stream.Close()
}

func TestHTTPStream_CloseNilBody(t *testing.T) {
	t.Parallel()
	s := &httpStream{}
	if err := s.Close(); err != nil {
		t.Errorf("Close nil body = %v", err)
	}
}

func TestIsResourceExpired_NonAPIError(t *testing.T) {
	t.Parallel()
	if IsResourceExpired(context.Canceled) {
		t.Error("context.Canceled is not a 410")
	}
	if IsResourceExpired(nil) {
		t.Error("nil is not a 410")
	}
}

func TestAPIError_Message(t *testing.T) {
	t.Parallel()
	e := &APIError{Status: 503}
	if e.Error() != "k8s watch: HTTP 503" {
		t.Errorf("Error() = %q", e.Error())
	}
}
