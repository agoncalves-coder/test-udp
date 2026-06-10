package transport

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/session"
)

// maxWSMessage acota los mensajes entrantes: chunks ≤ 1112 B y controles JSON
// chicos. Margen para no romper por overhead de framing.
const maxWSMessage = 4096

// WSHandler atiende GET /ws?session=<id>: liga la conexión a la sesión y corre
// el read-loop hasta que el cliente cierra o la sesión consolida.
func WSHandler(cfg config.Config, mgr *session.Manager, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	patterns := originHostPatterns(cfg.AllowedOrigins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := mgr.ByID(r.URL.Query().Get("session"))
		if !ok {
			http.Error(w, "sesión desconocida", http.StatusNotFound)
			return
		}

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: patterns,
			// Frames binarios de ~1.1 KB: comprimir solo agrega CPU en gama baja.
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			log.Warn("ws accept failed", "sessionId", s.ID, "err", err)
			return
		}
		c.SetReadLimit(maxWSMessage)
		s.SetTransport("websocket")
		log.Info("ws connected", "sessionId", s.ID)

		ctx := r.Context()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				// Cierre normal o abrupto: arranca la cuenta regresiva de
				// consolidación si no hubo END explícito.
				s.Reasm.NoteTransportClosed()
				log.Info("ws closed", "sessionId", s.ID, "reason", err.Error())
				return
			}
			switch typ {
			case websocket.MessageBinary:
				HandleDatagram(s, data)
			case websocket.MessageText:
				HandleControl(s, data, log)
			}
		}
	})
}

// originHostPatterns convierte orígenes CORS (https://host[:puerto]) en los
// patrones host[:puerto] que espera websocket.AcceptOptions.
func originHostPatterns(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		if u, err := url.Parse(strings.TrimSpace(o)); err == nil && u.Host != "" {
			out = append(out, u.Host)
		}
	}
	return out
}
