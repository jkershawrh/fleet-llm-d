package routing

import "testing"

func TestParseProviderName(t *testing.T) {
	tests := []struct {
		input string
		want  ProviderName
		ok    bool
	}{
		{"", ProviderPraxis, true},
		{"praxis", ProviderPraxis, true},
		{"llm-d-router", ProviderLLMD, true},
		{"disabled", ProviderNone, true},
		{"other", "", false},
	}
	for _, tt := range tests {
		got, err := ParseProviderName(tt.input)
		if (err == nil) != tt.ok || got != tt.want {
			t.Fatalf("ParseProviderName(%q) = (%q, %v), want (%q, ok=%v)", tt.input, got, err, tt.want, tt.ok)
		}
	}
}

func TestGridCRDTranslatorImplementsRoutingProvider(t *testing.T) {
	var provider RoutingProvider = NewGridCRDTranslator("https://k8s.invalid", "fleet", "", "grid")
	if provider.Name() != ProviderPraxis {
		t.Fatalf("provider.Name() = %q", provider.Name())
	}
}
