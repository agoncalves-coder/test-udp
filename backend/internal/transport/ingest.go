// Package transport adapta los canales (WebSocket, WebRTC DataChannel) al
// pipeline de reensamblado. La lógica de protocolo vive en UN solo lugar:
// los adaptadores solo rutean binario→HandleDatagram, texto→HandleControl.
package transport

import (
	"encoding/json"
	"log/slog"

	"face-capture-poc/backend/internal/protocol"
	"face-capture-poc/backend/internal/session"
)

// HandleDatagram procesa un mensaje binario (un chunk). La conexión ya está
// ligada a la sesión: cero lookups en el hot path, solo el assert de seq.
func HandleDatagram(s *session.Session, data []byte) {
	h, payload, err := protocol.DecodeChunk(data)
	if err != nil {
		s.Reasm.NoteProtocolError()
		return
	}
	if h.SessionSeq != s.Seq {
		s.Reasm.NoteWrongSession()
		return
	}
	s.MarkCapturing()
	s.Reasm.Ingest(h, payload)
}

// ControlMessage es el único mensaje de control del protocolo (texto JSON).
type ControlMessage struct {
	Type           string `json:"type"`
	SessionID      string `json:"sessionId"`
	FramesSent     int    `json:"framesSent"`
	ChunksSent     int    `json:"chunksSent"`
	CaptureStartMs int64  `json:"captureStartMs"`
	CaptureEndMs   int64  `json:"captureEndMs"`
}

// HandleControl procesa un mensaje de texto. end_of_capture es idempotente:
// el cliente lo manda 3× porque el canal puede perderlo.
func HandleControl(s *session.Session, text []byte, log *slog.Logger) {
	var msg ControlMessage
	if err := json.Unmarshal(text, &msg); err != nil {
		log.Warn("control message inválido", "sessionId", s.ID, "err", err)
		return
	}
	switch msg.Type {
	case "end_of_capture":
		s.Reasm.MarkEnd(msg.FramesSent)
		s.MarkEnding()
		log.Info("end of capture", "sessionId", s.ID, "framesSent", msg.FramesSent, "chunksSent", msg.ChunksSent)
	default:
		log.Warn("control message desconocido", "sessionId", s.ID, "type", msg.Type)
	}
}
