package datadogauth

import (
	"strings"
	"testing"
)

func TestCheckSite(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{
		"datadoghq.com", "us3.datadoghq.com", "us5.datadoghq.com",
		"ap1.datadoghq.com", "ap2.datadoghq.com", "datadoghq.eu", "ddog-gov.com",
	} {
		if err := CheckSite(ok); err != nil {
			t.Errorf("CheckSite(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"",
		"x@evil.example",
		"evil.example/x@datadoghq.com",
		"datadoghq.com@evil.example",
		"evil.example#.datadoghq.com",
		"evil.example?.datadoghq.com",
		"datadoghq.com:8443",
		"datadoghq.com.evil.example",
		"evildatadoghq.com",
		"evil.example",
		"us3.datadoghq.com.",
		".datadoghq.com",
		"us3..datadoghq.com",
		"-x.datadoghq.com",
		"us 3.datadoghq.com",
		"US3.DATADOGHQ.COM", // Resolve lowercases first; CheckSite takes lowercase only
		strings.Repeat("a", 64) + ".datadoghq.com",
		strings.Repeat("a.", 130) + "datadoghq.com",
	} {
		if err := CheckSite(bad); err == nil {
			t.Errorf("CheckSite(%q) = nil, want an error", bad)
		}
	}
}

func TestResolve_RefusesNonDatadogSite(t *testing.T) {
	t.Setenv(EnvSite, "")
	for _, site := range []string{"x@evil.example", "evil.example", "datadoghq.com:1"} {
		_, err := Resolve(ResolveOpts{APIKey: "test-datadog-api-key", AppKey: "test-datadog-app-key", Site: site})
		if err == nil {
			t.Errorf("Resolve(site %q) = nil error, want refusal", site)
			continue
		}
		if strings.Contains(err.Error(), "test-datadog") {
			t.Errorf("error leaks a key: %v", err)
		}
	}
	t.Setenv(EnvSite, "x@evil.example")
	if _, err := Resolve(ResolveOpts{APIKey: "test-datadog-api-key", AppKey: "test-datadog-app-key"}); err == nil {
		t.Error("Resolve with DATADOG_SITE=x@evil.example: want refusal")
	}
}

func TestResolve_LowercasesSite(t *testing.T) {
	t.Setenv(EnvSite, "")
	cred, err := Resolve(ResolveOpts{APIKey: "k", AppKey: "a", Site: " US3.DataDogHQ.com "})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL() != "https://api.us3.datadoghq.com" {
		t.Errorf("BaseURL = %q", cred.BaseURL())
	}
}
