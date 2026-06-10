package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// httpStream is the concrete Stream over a chunked watch HTTP response. The
// Kubernetes watch API returns a newline-delimited JSON stream of
// `{"type":..., "object":...}` frames (a "watch.Event" envelope). httpStream
// decodes one frame per Recv, mapping the decoded object into the caller's Raw
// shape via decodeObject.
//
// Read-only: the underlying request is GET ...?watch=true; it never mutates the
// cluster. The bearer token is set on the request and never logged here.
type httpStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	// decodeObject maps the raw JSON object of a frame into the collector's Raw
	// shape (rbac.RawBinding / workload.RawWorkload). Returning (nil, nil) drops a
	// frame the caller does not model.
	decodeObject func(raw json.RawMessage) (any, error)
}

// watchFrame is the Kubernetes watch envelope. We model only the fields the loop
// needs: the event type and the object (whose metadata carries the
// resourceVersion). An ERROR frame's object is a Status with a code/reason.
type watchFrame struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}

// frameMeta extracts the resourceVersion (and, for ERROR frames, the Status
// code) without committing to the resource shape.
type frameMeta struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	// Status-frame fields (present only on ERROR frames carrying a metav1.Status).
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// maxFrameBytes bounds one watch frame so a hostile/buggy server cannot drive an
// unbounded read for a single line. 4 MiB is far beyond any RBAC/workload object.
const maxFrameBytes = 4 << 20

// Recv decodes the next watch frame. It returns ErrStreamClosed on EOF (the
// server closed the watch normally — the loop re-watches). A 410 Gone Status
// frame is surfaced as an EventError with ResourceExpired=true.
func (s *httpStream) Recv() (Event, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			return Event{}, err
		}
		return Event{}, ErrStreamClosed
	}
	line := s.scanner.Bytes()
	if len(strings.TrimSpace(string(line))) == 0 {
		// Keep-alive blank line; report a no-op bookmark-less frame by recursing to
		// the next line. Bounded by the stream ending.
		return s.Recv()
	}
	var f watchFrame
	if err := json.Unmarshal(line, &f); err != nil {
		return Event{}, fmt.Errorf("watch: decode frame: %w", err)
	}
	var meta frameMeta
	// Object metadata is best-effort; a frame without parseable metadata still
	// yields its type.
	_ = json.Unmarshal(f.Object, &meta)

	switch EventType(f.Type) {
	case EventError:
		// A 410 Gone reports resourceVersion-too-old (IsResourceExpired).
		return Event{Type: EventError, ResourceExpired: meta.Code == http.StatusGone}, nil
	case EventBookmark:
		return Event{Type: EventBookmark, ResourceVersion: meta.Metadata.ResourceVersion}, nil
	case EventAdded, EventModified, EventDeleted:
		obj, err := s.decodeObject(f.Object)
		if err != nil {
			return Event{}, fmt.Errorf("watch: decode object: %w", err)
		}
		return Event{Type: EventType(f.Type), ResourceVersion: meta.Metadata.ResourceVersion, Object: obj}, nil
	default:
		// Unknown type: surface it so the loop can log+ignore.
		return Event{Type: EventType(f.Type), ResourceVersion: meta.Metadata.ResourceVersion}, nil
	}
}

// Close releases the watch HTTP body.
func (s *httpStream) Close() error {
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}

// HTTPSource is the concrete read-only Source over the Kubernetes API. It LISTs a
// resource path to bootstrap the resourceVersion and opens a watch stream from a
// given RV. It holds a short-lived bearer token it never logs.
//
// It adds NO new resource and NO new verb beyond `watch` (alongside the existing
// get,list on the same surfaces). List is GET <path>?limit=...; Watch is GET
// <path>?watch=true&allowWatchBookmarks=true&resourceVersion=<rv>.
type HTTPSource struct {
	http    *http.Client
	baseURL string
	token   string
	path    string
	// listObjects decodes the LIST response items into the collector's Raw shape
	// and returns the list resourceVersion (from the list's metadata). decodeFrame
	// decodes one watch frame's object.
	listObjects  func(body io.Reader) (objects []any, resourceVersion string, err error)
	decodeObject func(raw json.RawMessage) (any, error)
}

// HTTPSourceConfig configures a HTTPSource.
type HTTPSourceConfig struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
	// Path is the resource collection path, e.g.
	// "/apis/rbac.authorization.k8s.io/v1/rolebindings".
	Path string
	// ListObjects decodes the LIST body into Raw objects + the list RV.
	ListObjects func(body io.Reader) (objects []any, resourceVersion string, err error)
	// DecodeObject decodes one watch frame's object JSON into a Raw object.
	DecodeObject func(raw json.RawMessage) (any, error)
}

// NewHTTPSource builds a read-only watch Source. A nil HTTP client gets a
// no-timeout default — a watch stream is intentionally long-lived, so the
// per-request timeout the list/pull path uses must NOT apply to the watch GET
// (it would tear the stream every N seconds). The run context bounds the watch
// instead.
func NewHTTPSource(cfg HTTPSourceConfig) *HTTPSource {
	hc := cfg.HTTP
	if hc == nil {
		// No client timeout: the watch is long-lived; ctx bounds it.
		hc = &http.Client{}
	}
	return &HTTPSource{
		http:         hc,
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		token:        cfg.Token,
		path:         cfg.Path,
		listObjects:  cfg.ListObjects,
		decodeObject: cfg.DecodeObject,
	}
}

// List issues a read-only GET against the resource path and decodes the current
// objects + the list resourceVersion to watch from.
func (h *HTTPSource) List(ctx context.Context) ([]any, string, error) {
	u := fmt.Sprintf("%s%s?limit=500", h.baseURL, h.path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	h.auth(req)
	res, err := h.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, "", &APIError{Status: res.StatusCode}
	}
	return h.listObjects(res.Body)
}

// Watch opens a read-only watch stream from resourceVersion with
// allowWatchBookmarks=true.
func (h *HTTPSource) Watch(ctx context.Context, resourceVersion string) (Stream, error) {
	q := url.Values{}
	q.Set("watch", "true")
	q.Set("allowWatchBookmarks", "true")
	if resourceVersion != "" {
		q.Set("resourceVersion", resourceVersion)
	}
	u := fmt.Sprintf("%s%s?%s", h.baseURL, h.path, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	h.auth(req)
	res, err := h.http.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		code := res.StatusCode
		_ = res.Body.Close()
		return nil, &APIError{Status: code}
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameBytes)
	return &httpStream{body: res.Body, scanner: sc, decodeObject: h.decodeObject}, nil
}

func (h *HTTPSource) auth(req *http.Request) {
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	req.Header.Set("Accept", "application/json")
}

// APIError carries the watch HTTP status. Mirrors k8slist.APIError but is local
// so the watch package does not couple to the list reader's error shape.
type APIError struct {
	Status int
}

func (e *APIError) Error() string { return fmt.Sprintf("k8s watch: HTTP %d", e.Status) }

// IsResourceExpired reports whether err is a 410 Gone (resourceVersion too old).
func IsResourceExpired(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusGone
	}
	return false
}
