package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementAPIRequiresToken(t *testing.T) {
	manager, err := NewManager(Options{Home: t.TempDir(), ProxyListen: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	token := "0123456789abcdefghij0123456789"
	handler := NewHandler(manager, token)
	for _, test := range []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Bearer " + token, http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/subscriptions", nil)
		if test.header != "" {
			req.Header.Set("Authorization", test.header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.want {
			t.Errorf("header=%q status=%d want=%d", test.header, response.Code, test.want)
		}
	}
}

func TestProxyLANValidation(t *testing.T) {
	cases := []struct {
		opts  Options
		valid bool
	}{
		{Options{ProxyListen: "127.0.0.1"}, true},
		{Options{ProxyListen: "0.0.0.0", AllowLAN: true}, false},
		{Options{ProxyListen: "192.168.1.210"}, false},
		{Options{ProxyListen: "192.168.1.210", AllowLAN: true}, true},
		{Options{ProxyListen: "192.168.1.210", AllowLAN: true, AllowedCIDRs: []string{"192.168.1.0/24"}}, true},
		{Options{ProxyListen: "192.168.1.210", AllowLAN: true, AllowedCIDRs: []string{"0.0.0.0/0"}}, false},
	}
	for i, c := range cases {
		c.opts.Home = t.TempDir()
		_, err := NewManager(c.opts)
		if (err == nil) != c.valid {
			t.Errorf("case %d err=%v valid=%v", i, err, c.valid)
		}
	}
}

func TestSubscriptionURLValidation(t *testing.T) {
	for _, valid := range []string{"https://example.org/sub/token", "http://example.org/sub"} {
		if err := validateSubscriptionURL(valid); err != nil {
			t.Errorf("unexpected error for %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"file:///etc/passwd", "https://user:pass@example.org/sub", "https://example.org/#secret", "not-a-url"} {
		if err := validateSubscriptionURL(invalid); err == nil {
			t.Errorf("accepted invalid URL %q", invalid)
		}
	}
}
