package cloudflare

import "testing"

func TestExtractCloudflareAccountID(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"standard path", "https://api.cloudflare.com/client/v4/accounts/ACC123", "ACC123"},
		{"custom host", "http://example.com/client/v4/accounts/my-account", "my-account"},
		{"trailing slash", "https://api.cloudflare.com/client/v4/accounts/ACC123/", "ACC123"},
		{"query suffix", "https://api.cloudflare.com/client/v4/accounts/ACC123?x=1", "ACC123"},
		{"no account segment", "https://api.cloudflare.com/client/v4", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractCloudflareAccountID(tc.baseURL); got != tc.want {
				t.Fatalf("extractCloudflareAccountID(%q) = %q, want %q", tc.baseURL, got, tc.want)
			}
		})
	}
}
