package cloud

import "testing"

func TestParseProvider(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want Provider
		ok   bool
	}{
		{"local-ollama", ProviderLocalOllama, true},
		{"LOCAL-OLLAMA", ProviderLocalOllama, true},
		{"  anthropic  ", ProviderAnthropic, true},
		{"openai", ProviderOpenAI, true},
		{"bedrock", ProviderBedrock, true},
		{"gpt-4", "", false},
		{"https://evil.example/v1", "", false}, // no free-text URL (P0-499-3)
		{"", "", false},
		{"custom", "", false},
	}
	for _, c := range cases {
		got, ok := ParseProvider(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ParseProvider(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestProvider_IsCloud(t *testing.T) {
	t.Parallel()
	if ProviderLocalOllama.IsCloud() {
		t.Error("local-ollama reported as cloud")
	}
	for _, p := range []Provider{ProviderAnthropic, ProviderOpenAI, ProviderBedrock} {
		if !p.IsCloud() {
			t.Errorf("%q not reported as cloud", p)
		}
	}
	if Provider("bogus").IsCloud() {
		t.Error("unknown provider reported as cloud")
	}
}

func TestIsCloudProvider_String(t *testing.T) {
	t.Parallel()
	local := []string{"", "ollama", "ollama-local", "local", "local-ollama", "stub", "STUB"}
	for _, s := range local {
		if IsCloudProvider(s) {
			t.Errorf("IsCloudProvider(%q) = true, want false", s)
		}
	}
	cloud := []string{"anthropic", "openai", "bedrock", "Anthropic"}
	for _, s := range cloud {
		if !IsCloudProvider(s) {
			t.Errorf("IsCloudProvider(%q) = false, want true", s)
		}
	}
}
