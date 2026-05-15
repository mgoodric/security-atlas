package main

import "testing"

// TestDeriveHTTPEndpoint covers the small heuristic that converts the
// gRPC --endpoint into the conventional HTTP endpoint for the
// reset-bootstrap admin call.
func TestDeriveHTTPEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// :50051 -> :8080 swap, http scheme prepended.
		{in: "atlas:50051", want: "http://atlas:8080"},
		{in: "localhost:50051", want: "http://localhost:8080"},
		// Existing http:// scheme is passed through.
		{in: "http://atlas:8080", want: "http://atlas:8080"},
		// Existing https:// scheme is passed through.
		{in: "https://atlas.example.com", want: "https://atlas.example.com"},
		// Unrecognised port → empty (caller must pass --http-endpoint).
		{in: "atlas:1234", want: ""},
	}
	for _, c := range cases {
		got := deriveHTTPEndpoint(c.in)
		if got != c.want {
			t.Errorf("deriveHTTPEndpoint(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}
