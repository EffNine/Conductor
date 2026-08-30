package security

import (
	"net"
	"testing"
)

func TestValidateProviderURL_BlocksPrivate(t *testing.T) {
	block := []struct {
		url  string
		priv bool
	}{
		{"http://127.0.0.1/v1", false},
		{"https://127.0.0.1/v1", false},
		{"http://localhost:11434/v1", false},
		{"https://10.0.0.1/v1", false},
		{"https://192.168.1.10/v1", false},
		{"https://172.16.5.4/v1", false},
		{"https://172.31.255.255/v1", false},
		{"https://169.254.169.254/latest/meta-data/", false},
		{"https://0.0.0.0/v1", false},
		{"https://192.0.2.1/v1", false},
		{"https://[::1]/v1", false},
		{"https://[fc00::1]/v1", false},
		{"https://[fe80::1]/v1", false},
		{"http://0x7f.0.0.1/v1", false},
		{"https://2130706433/v1", false}, // 127.0.0.1 decimal
	}
	for _, tc := range block {
		err := ValidateProviderURL(tc.url, tc.priv)
		if err == nil {
			t.Errorf("expected block for %q, got nil", tc.url)
		}
	}
}

func TestValidateProviderURL_AllowsPublic(t *testing.T) {
	allow := []string{
		"https://api.openai.com/v1",
		"https://api.anthropic.com/v1",
		"https://generativelanguage.googleapis.com/v1beta",
		"https://api.deepseek.com/v1",
		"https://integrate.api.nvidia.com/v1",
		"https://openrouter.ai/api/v1",
	}
	for _, u := range allow {
		if err := ValidateProviderURL(u, false); err != nil {
			t.Errorf("expected allow for %q, got %v", u, err)
		}
	}
}

func TestValidateProviderURL_AllowsLocalWhenFlag(t *testing.T) {
	if err := ValidateProviderURL("http://localhost:11434/v1", true); err != nil {
		t.Fatalf("localhost should be allowed with allowPrivate: %v", err)
	}
	if err := ValidateProviderURL("http://127.0.0.1:11434/v1", true); err != nil {
		t.Fatalf("127.0.0.1 should be allowed with allowPrivate: %v", err)
	}
	// but still block link-local even with allowPrivate=false
	if err := ValidateProviderURL("https://169.254.169.254/", false); err == nil {
		t.Fatal("metadata should be blocked")
	}
}

func TestValidateProviderURL_RejectsBadSchemeAndUserInfo(t *testing.T) {
	if err := ValidateProviderURL("ftp://api.openai.com/v1", false); err == nil {
		t.Fatal("ftp should be blocked")
	}
	if err := ValidateProviderURL("https://user:pass@api.openai.com/v1", false); err == nil {
		t.Fatal("userinfo should be blocked")
	}
	if err := ValidateProviderURL("https://api.openai.com/v1", false); err != nil {
		t.Fatalf("valid https should pass: %v", err)
	}
	// http not allowed for public
	if err := ValidateProviderURL("http://api.openai.com/v1", false); err == nil {
		t.Fatal("http for public provider should be blocked")
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := map[string]bool{
		"127.0.0.1":       true,
		"10.0.0.5":        true,
		"192.168.1.1":     true,
		"172.16.0.1":      true,
		"172.31.255.255":  true,
		"172.32.0.1":      false,
		"8.8.8.8":         false,
		"1.1.1.1":         false,
		"169.254.169.254": true,
		"0.0.0.0":         true,
		"192.0.2.1":       true,
		"::1":             true,
		"fc00::1":         true,
		"fe80::1":         true,
		"ff02::1":         true,
		"2001:db8::1":     true,
		"2606:4700::1111": false,
	}
	for ipStr, wantPrivate := range tests {
		// use real net.ParseIP via helper that imports net
		// we need to import net, but already imported indirectly via security package
		// call via direct parse using net in this file's import
		ip := parseIPReal(ipStr)
		if ip == nil {
			t.Fatalf("parse %q", ipStr)
		}
		got := IsPrivateIP(ip)
		if got != wantPrivate {
			t.Errorf("IsPrivateIP(%q)=%v want %v", ipStr, got, wantPrivate)
		}
	}
}

// parseIPReal uses net.ParseIP — we define here to avoid import cycle confusion
func parseIPReal(s string) net.IP {
	// net is already imported in ssrf.go, but not in this test file? we import it now
	return net.ParseIP(s)
}
