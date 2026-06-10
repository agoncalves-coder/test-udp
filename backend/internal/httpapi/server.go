// Package httpapi expone la API REST de sesiones y experimentos sobre la
// stdlib (net/http con patrones de método de Go 1.22+; sin frameworks).
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/session"
)

type Server struct {
	cfg config.Config
	mgr *session.Manager
	log *slog.Logger
}

// Handler arma el mux completo. wsHandler (puede ser nil en tests) se monta en
// GET /ws; signalHandler (Fase 2, puede ser nil) en POST /signal.
func Handler(cfg config.Config, mgr *session.Manager, log *slog.Logger, wsHandler, signalHandler http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{cfg: cfg, mgr: mgr, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /experiments", s.handleExperiments)
	mux.HandleFunc("POST /sessions", s.handleCreateSession)
	mux.HandleFunc("GET /sessions/{id}", s.handleGetSession)
	mux.HandleFunc("GET /sessions/{id}/frames", s.handleListFrames)
	mux.HandleFunc("GET /sessions/{id}/frames/{file}", s.handleServeFrame)
	mux.HandleFunc("GET /sessions/{id}/report.json", s.handleReport)
	mux.HandleFunc("POST /sessions/{id}/client-stats", s.handleClientStats)
	if wsHandler != nil {
		mux.Handle("GET /ws", wsHandler)
	}
	if signalHandler != nil {
		mux.Handle("POST /signal", signalHandler)
	}

	return cors(cfg.AllowedOrigins, mux)
}

// cors: echo del origin exacto permitido (nunca *), Vary: Origin, y respuesta
// 204 al preflight. Los POST con Content-Type: application/json se preflightean.
func cors(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(allowed, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
