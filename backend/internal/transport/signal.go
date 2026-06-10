package transport

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/pion/webrtc/v4"

	"face-capture-poc/backend/internal/session"
)

const maxSignalBody = 256 << 10 // una SDP con candidatos pesa pocos KB

// SignalHandler atiende POST /signal: señalización non-trickle en un único
// round-trip HTTP (PRD §3.3). El server no configura STUN propio, así que su
// gathering completa al instante y la answer ya incluye todos los candidatos.
func SignalHandler(engine *WebRTCEngine, mgr *session.Manager, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SessionID string                    `json:"sessionId"`
			Offer     webrtc.SessionDescription `json:"offer"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, maxSignalBody)).Decode(&req); err != nil {
			http.Error(w, `{"error":"json inválido"}`, http.StatusBadRequest)
			return
		}
		s, ok := mgr.ByID(req.SessionID)
		if !ok {
			http.Error(w, `{"error":"sesión desconocida"}`, http.StatusNotFound)
			return
		}

		answer, err := engine.CreateAnswer(r.Context(), s, req.Offer)
		if err != nil {
			log.Error("signal failed", "sessionId", s.ID, "err", err)
			http.Error(w, `{"error":"negociación webrtc falló"}`, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"answer": answer})
	})
}
