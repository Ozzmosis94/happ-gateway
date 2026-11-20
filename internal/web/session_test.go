package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionCookieSurvivesHandlerRecreation(t *testing.T) {
	token := strings.Repeat("t", 32)
	m, err := NewManager(Options{Home: t.TempDir()})
	if err != nil { t.Fatal(err) }
	h1 := NewHandler(m, token)
	body, _ := json.Marshal(map[string]string{"token": token})
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h1.ServeHTTP(response, request)
	if response.Code != http.StatusOK { t.Fatalf("login status=%d body=%s", response.Code, response.Body.String()) }
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("invalid session cookie: %+v", cookies)
	}
	if cookies[0].Value == token { t.Fatal("cookie must not contain access token") }

	// A new handler on the same persisted token represents a container restart.
	h2 := NewHandler(m, token)
	check := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	check.AddCookie(cookies[0])
	w := httptest.NewRecorder()
	h2.ServeHTTP(w, check)
	if w.Code != http.StatusOK { t.Fatalf("session was lost after handler recreation: %d", w.Code) }

	// A different token invalidates all old cookies.
	h3 := NewHandler(m, strings.Repeat("x", 32))
	w = httptest.NewRecorder()
	h3.ServeHTTP(w, check)
	if w.Code != http.StatusUnauthorized { t.Fatalf("expected 401 after token change, got %d", w.Code) }
}

func TestExpiredSessionAndCrossOriginRejected(t *testing.T) {
	token := strings.Repeat("t", 32)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	setSessionCookie(rec, req, token, time.Now().Add(-time.Hour))
	cookie := rec.Result().Cookies()[0]
	if validSession(cookie.Value, token, time.Now()) { t.Fatal("expired session accepted") }

	m, err := NewManager(Options{Home: t.TempDir()})
	if err != nil { t.Fatal(err) }
	h := NewHandler(m, token)
	login := httptest.NewRequest(http.MethodPost, "http://gateway.local/api/login", bytes.NewBufferString(`{"token":"`+token+`"}`))
	login.Header.Set("Origin", "http://evil.local")
	out := httptest.NewRecorder()
	h.ServeHTTP(out, login)
	if out.Code != http.StatusForbidden { t.Fatalf("cross-origin login status=%d", out.Code) }
}
