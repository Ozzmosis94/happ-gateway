package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
 "net"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed static/*
var staticFiles embed.FS

const sessionCookie = "happ_gateway_session"
const sessionDuration = 30 * 24 * time.Hour

type ErrorResponse struct {
	Error string `json:"error"`
}

func NewHandler(m *Manager, token string, creds *credentialStore, sessionKey string, migrationRequired bool) http.Handler {
 throttle:=newLoginThrottle()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		data, _ := staticFiles.ReadFile("static/index.html")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /app.js", asset("static/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /style.css", asset("static/style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter,r *http.Request){
  writeJSON(w,http.StatusOK,map[string]bool{"configured":creds.configured(),"migrationRequired":migrationRequired && !creds.configured()})
 })
 mux.HandleFunc("POST /api/setup",func(w http.ResponseWriter,r *http.Request){
  if !sameOrigin(r){writeError(w,403,"cross-origin request");return}
  var input struct{ Username string `json:"username"`; Password string `json:"password"`; MigrationToken string `json:"migrationToken"` }
  if !decodeJSON(w,r,&input){return}
  if creds.configured(){writeError(w,409,"administrator already configured");return}
  if migrationRequired && subtle.ConstantTimeCompare([]byte(input.MigrationToken),[]byte(token))!=1 {writeError(w,401,"existing installation requires original access token");return}
  if err:=creds.setup(input.Username,input.Password);err!=nil{writeError(w,400,err.Error());return}
  setSessionCookie(w,r,sessionKey,time.Now().Add(sessionDuration))
  writeJSON(w,201,map[string]bool{"ok":true})
 })
 mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
  if !sameOrigin(r) {writeError(w,403,"cross-origin request");return}
  if !creds.configured(){writeError(w,403,"complete first-run setup");return}
  host,_,err:=net.SplitHostPort(r.RemoteAddr);if err!=nil{host=r.RemoteAddr}
  if !throttle.allowed(host){writeError(w,429,"too many attempts, retry later");return}
  var input struct{ Username string `json:"username"`; Password string `json:"password"` }
  if !decodeJSON(w,r,&input){return}
  ok:=creds.verify(input.Username,input.Password)
  throttle.result(host,ok)
  if !ok{writeError(w,401,"invalid username or password");return}
  setSessionCookie(w,r,sessionKey,time.Now().Add(sessionDuration))
  writeJSON(w,200,map[string]bool{"ok":true})
 })
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/api/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.Handle("/api/", auth(sessionKey, apiHandler(m)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, secret string, expires time.Time) {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("happ-gateway-session:" + exp))
	value := exp + "." + hex.EncodeToString(mac.Sum(nil))
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/api/", HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
		Expires: expires, MaxAge: int(sessionDuration.Seconds()),
	})
}

func validSession(value, secret string, now time.Time) bool {
	exp, sig, ok := strings.Cut(value, ".")
	if !ok {
		return false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() >= unix || unix > now.Add(sessionDuration).Unix() {
		return false
	}
	provided, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("happ-gateway-session:" + exp))
	return hmac.Equal(provided, mac.Sum(nil))
}

func sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // CLI / curl callers do not send Origin.
	}
	// Avoid trusting Forwarded headers: the gateway does not proxy requests.
	wantScheme := "http"
	if r.TLS != nil {
		wantScheme = "https"
	}
	return origin == wantScheme+"://"+r.Host
}

func asset(file, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", kind)
		data, _ := staticFiles.ReadFile(file)
		_, _ = w.Write(data)
	}
}

func auth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin requests are not allowed")
			return
		}
		if cookie, err := r.Cookie(sessionCookie); err == nil && validSession(cookie.Value, token, time.Now()) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
	})
}

func apiHandler(m *Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, m.Status()) })
	mux.HandleFunc("GET /api/subscriptions", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, m.Subscriptions()) })
	mux.HandleFunc("POST /api/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := m.Add(r.Context(), input.Name, input.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/subscriptions/{name}/servers", func(w http.ResponseWriter, r *http.Request) {
		servers, err := m.Servers(r.PathValue("name"))
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, servers)
	})
	mux.HandleFunc("POST /api/subscriptions/{name}/activate", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Activate(r.PathValue("name")); err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/subscriptions/{name}/refresh", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Force bool `json:"force"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		err := m.Update(r.Context(), r.PathValue("name"), input.Force)
		if errors.Is(err, ErrLargeDrop) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("DELETE /api/subscriptions/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Remove(r.PathValue("name")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/connect", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Subscription string `json:"subscription"`
			Index        int    `json:"index"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := m.Connect(input.Subscription, input.Index); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m.Status())
	})
	mux.HandleFunc("POST /api/disconnect", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Disconnect(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, m.Status())
	})
	return mux
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "only one JSON object is allowed")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}
