package secretmeta

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeAPI struct {
	secrets []RawSecret
	err     error
}

func (f *fakeAPI) ListSecretMeta(_ context.Context) ([]RawSecret, error) {
	return f.secrets, f.err
}

func fixedNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) }
}

func TestCollect_MetadataPreserved(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC) // 30 days before fixedNow
	api := &fakeAPI{secrets: []RawSecret{{
		Namespace: "prod",
		Name:      "web-tls",
		Type:      "kubernetes.io/tls",
		CreatedAt: created,
		KeyNames:  []string{"tls.key", "tls.crt"},
	}}}
	got, err := Collect(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d; want 1", len(got))
	}
	inv := got[0]
	if inv.Namespace != "prod" || inv.Name != "web-tls" || inv.Type != "kubernetes.io/tls" {
		t.Errorf("metadata not preserved: %+v", inv)
	}
	if inv.AgeDays != 30 {
		t.Errorf("age = %d days; want 30", inv.AgeDays)
	}
	// KeyNames sorted.
	if strings.Join(inv.KeyNames, ",") != "tls.crt,tls.key" {
		t.Errorf("key names = %v; want sorted [tls.crt tls.key]", inv.KeyNames)
	}
}

func TestCollect_EmptyTypeNormalizesToOpaque(t *testing.T) {
	t.Parallel()
	got, err := Collect(context.Background(), &fakeAPI{secrets: []RawSecret{
		{Namespace: "default", Name: "config", Type: "", KeyNames: []string{"a"}},
	}}, fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got[0].Type != "Opaque" {
		t.Errorf("empty type = %q; want Opaque", got[0].Type)
	}
}

func TestCollect_AgeClampsNonNegative(t *testing.T) {
	t.Parallel()
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	got, _ := Collect(context.Background(), &fakeAPI{secrets: []RawSecret{
		{Namespace: "ns", Name: "s", CreatedAt: future, KeyNames: []string{"k"}},
	}}, fixedNow())
	if got[0].AgeDays != 0 {
		t.Errorf("future-dated secret age = %d; want clamped 0", got[0].AgeDays)
	}
}

func TestCollect_ZeroCreatedAtAgeZero(t *testing.T) {
	t.Parallel()
	got, _ := Collect(context.Background(), &fakeAPI{secrets: []RawSecret{
		{Namespace: "ns", Name: "s", KeyNames: []string{"k"}},
	}}, fixedNow())
	if got[0].AgeDays != 0 {
		t.Errorf("zero CreatedAt age = %d; want 0", got[0].AgeDays)
	}
}

func TestCollect_SkipsUnidentifiedSecrets(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{secrets: []RawSecret{
		{Namespace: "", Name: "no-ns"},
		{Namespace: "ns", Name: ""},
		{Namespace: "ns", Name: "ok"},
	}}
	got, err := Collect(context.Background(), api, fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok" {
		t.Fatalf("want only the fully-identified secret; got %+v", got)
	}
}

func TestCollect_BoundedByCap(t *testing.T) {
	t.Parallel()
	over := maxSecrets + 25
	raw := make([]RawSecret, 0, over)
	for i := 0; i < over; i++ {
		raw = append(raw, RawSecret{Namespace: "ns", Name: "s" + itoa(i)})
	}
	got, err := Collect(context.Background(), &fakeAPI{secrets: raw}, fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != maxSecrets {
		t.Errorf("collected = %d; want capped at %d", len(got), maxSecrets)
	}
}

func TestCollect_NilAPI(t *testing.T) {
	t.Parallel()
	if _, err := Collect(context.Background(), nil, nil); err == nil {
		t.Fatal("want error on nil API")
	}
}

func TestCollect_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("403")
	if _, err := Collect(context.Background(), &fakeAPI{err: sentinel}, nil); !errors.Is(err, sentinel) {
		t.Fatalf("want wrapped sentinel; got %v", err)
	}
}

func TestCollect_DefaultNow(t *testing.T) {
	t.Parallel()
	got, _ := Collect(context.Background(), &fakeAPI{secrets: []RawSecret{{Namespace: "ns", Name: "s"}}}, nil)
	if got[0].ObservedAt.IsZero() {
		t.Error("observedAt should be set")
	}
}

// TestStruct_MetadataOnly_NoValueBearingFields is the LOAD-BEARING structural
// over-collection guard (AC-5). It reflects over every field name of RawSecret +
// Inventory and FAILS if any field name hints at a Secret VALUE / data / content
// / payload surface, so a future field that opens a value-leak door trips the
// build. The structs may carry ONLY: namespace, name, type, creation/age, key
// NAMES, and observation time. The substring "data" is banned EXCEPT where it is
// part of the permitted "CreatedAt" field — handled via the allow set.
func TestStruct_MetadataOnly_NoValueBearingFields(t *testing.T) {
	t.Parallel()
	banned := []string{
		"value", "values", "data", "stringdata", "secret", "payload",
		"content", "raw", "base64", "decoded", "plaintext", "blob", "bytes",
		"password", "token", "credential", "cert", "key", "annotation",
	}
	// allow lists field names that legitimately contain a banned substring but
	// are NOT a value surface. "KeyNames" contains "key" but holds only key
	// NAMES (map keys), never values. "CreatedAt" contains "data"? no — but
	// guard generically.
	allow := map[string]bool{
		"keynames": true, // map KEYS only — names, never values
	}
	check := func(typ reflect.Type) {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			if allow[name] {
				continue
			}
			for _, b := range banned {
				if strings.Contains(name, b) {
					t.Errorf("%s.%s: field name contains banned value-surface token %q — secret-inventory structs must carry only metadata (namespace/name/type/age/key-NAMES), NEVER a Secret value",
						typ.Name(), typ.Field(i).Name, b)
				}
			}
		}
	}
	check(reflect.TypeOf(RawSecret{}))
	check(reflect.TypeOf(Inventory{}))
}

// TestReduce_DropsSecretValues is the AC-5 client-boundary proof: feed a
// Kubernetes Secret JSON with REAL .data (base64) + .stringData (plaintext) and
// assert that ONLY type/namespace/name/age/key-NAMES survive — no value, raw or
// base64-decoded, ever enters the RawSecret. The fixture values are obviously
// fake neutral strings (no vendor-shaped token), so the branch-scoped secret
// scanner does not flag them.
func TestReduce_DropsSecretValues(t *testing.T) {
	t.Parallel()
	// Obviously-fake neutral values. base64("test-secret-value-should-be-dropped")
	// and a plaintext stringData value. Neither is a vendor-shaped literal.
	dropMarkerB64 := base64.StdEncoding.EncodeToString([]byte("test-secret-value-should-be-dropped"))
	const stringDataMarker = "test-stringdata-plaintext-should-be-dropped"
	secretJSON := []byte(`{
	  "metadata": {"name": "web-tls", "namespace": "prod", "creationTimestamp": "2026-05-10T00:00:00Z", "annotations": {"a": "should-not-be-read"}},
	  "type": "kubernetes.io/tls",
	  "data": {"tls.crt": "` + dropMarkerB64 + `", "tls.key": "` + dropMarkerB64 + `"},
	  "stringData": {"extra": "` + stringDataMarker + `"}
	}`)

	var s apiSecret
	if err := json.Unmarshal(secretJSON, &s); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	raw := reduce(s)

	// Metadata survives.
	if raw.Namespace != "prod" || raw.Name != "web-tls" || raw.Type != "kubernetes.io/tls" {
		t.Errorf("metadata not preserved: %+v", raw)
	}
	if strings.Join(raw.KeyNames, ",") != "tls.crt,tls.key" {
		t.Errorf("key names = %v; want [tls.crt tls.key] (the MAP KEYS only)", raw.KeyNames)
	}

	// No value — raw OR base64-decoded — anywhere in the reduced struct.
	assertNoValueLeak(t, raw, dropMarkerB64, stringDataMarker)

	// And through the full Collect -> Inventory transform too.
	got, err := Collect(context.Background(), &fakeAPI{secrets: []RawSecret{raw}}, fixedNow())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	assertNoValueLeak(t, got[0], dropMarkerB64, stringDataMarker)
}

// assertNoValueLeak serializes the whole record and asserts NONE of the secret
// markers — the base64 form, the base64-DECODED form, or the stringData
// plaintext — appears anywhere. This is the value-never-materializes proof.
func assertNoValueLeak(t *testing.T, record any, b64Marker, stringDataMarker string) {
	t.Helper()
	blob, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	hay := string(blob)

	decoded, _ := base64.StdEncoding.DecodeString(b64Marker)
	for _, needle := range []string{b64Marker, string(decoded), stringDataMarker, "should-not-be-read"} {
		if needle != "" && strings.Contains(hay, needle) {
			t.Fatalf("SECRET VALUE LEAKED into a record (found %q): %s", needle, hay)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
