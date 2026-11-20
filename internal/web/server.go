package web

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

//go:embed static/*
var staticFiles embed.FS

type ErrorResponse struct {
	Error string `json:"error"`
}

func NewHandler(m *Manager, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		data, _ := staticFiles.ReadFile("static/index.html")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /app.js", asset("static/app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("GET /style.css", asset("static/style.css", "text/css; charset=utf-8"))
	mux.Handle("/api/", auth(token, apiHandler(m)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
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
		provided, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
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
