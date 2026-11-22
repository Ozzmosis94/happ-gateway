package web

import (
 "strings"
	"net/http/httptest"
	"testing"
)

func TestPasswordSetupAndLogin(t *testing.T) {
 manager,err:=NewManager(Options{Home:t.TempDir(),ProxyListen:"127.0.0.1"})
 if err!=nil{t.Fatal(err)}
 creds,err:=newCredentialStore(t.TempDir());if err!=nil{t.Fatal(err)}
 handler:=NewHandler(manager,"",creds,"12345678901234567890123456789012",false)
 request:=func(method,path,body,cookie string)*httptest.ResponseRecorder{
  req:=httptest.NewRequest(method,path,strings.NewReader(body))
  if cookie!=""{req.Header.Set("Cookie",cookie)}
  out:=httptest.NewRecorder();handler.ServeHTTP(out,req);return out
 }
 if got:=request("GET","/api/subscriptions","","");got.Code!=401{t.Fatalf("unauth status %d",got.Code)}
 setup:=request("POST","/api/setup",`{"username":"admin","password":"correct-horse-battery"}`,"")
 if setup.Code!=201{t.Fatalf("setup status=%d body=%s",setup.Code,setup.Body.String())}
 if got:=request("POST","/api/setup",`{"username":"second","password":"correct-horse-battery"}`,"");got.Code!=409{t.Fatalf("second setup=%d",got.Code)}
 if got:=request("POST","/api/login",`{"username":"admin","password":"incorrect-password"}`,"");got.Code!=401{t.Fatalf("bad password=%d",got.Code)}
 login:=request("POST","/api/login",`{"username":"admin","password":"correct-horse-battery"}`,"")
 if login.Code!=200{t.Fatalf("login=%d",login.Code)}
 cookie:=login.Result().Cookies()[0]
 if got:=request("GET","/api/subscriptions","",cookie.String());got.Code!=200{t.Fatalf("authenticated status=%d",got.Code)}
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
