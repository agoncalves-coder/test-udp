// Package session modela una sesión de captura y su ciclo de vida:
// open → capturing (primer chunk) → ending (END recibido) → done (consolidada).
// Las sesiones son independientes entre sí (PRD §12.3).
package session

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"face-capture-poc/backend/internal/experiments"
	"face-capture-poc/backend/internal/reassembler"
	"face-capture-poc/backend/internal/storage"
)

type Session struct {
	ID        string
	Seq       uint16
	Preset    experiments.Preset
	CreatedAt time.Time
	Reasm     *reassembler.Reassembler
	Store     *storage.SessionStore

	log *slog.Logger

	mu          sync.Mutex
	state       string // open | capturing | ending | done
	transport   string // "" | websocket | webrtc
	clientStats json.RawMessage
	report      *reassembler.Report

	// done se cierra al consolidar; el sweep-loop lo usa para terminar.
	done chan struct{}
}

func (s *Session) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Session) Transport() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport
}

// SetTransport registra el canal ganador ("websocket" | "webrtc").
func (s *Session) SetTransport(kind string) {
	s.mu.Lock()
	s.transport = kind
	s.mu.Unlock()
	s.log.Info("transport bound", "sessionId", s.ID, "transport", kind)
}

// MarkCapturing pasa open→capturing (primer chunk recibido).
func (s *Session) MarkCapturing() {
	s.mu.Lock()
	if s.state == "open" {
		s.state = "capturing"
	}
	s.mu.Unlock()
}

// MarkEnding pasa a ending (END_OF_CAPTURE recibido).
func (s *Session) MarkEnding() {
	s.mu.Lock()
	if s.state == "open" || s.state == "capturing" {
		s.state = "ending"
	}
	s.mu.Unlock()
}

// Report devuelve el report final (nil hasta consolidar).
func (s *Session) Report() *reassembler.Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report
}

// SetClientStats adjunta las métricas del cliente (POST client-stats). Actúa
// además como señal de fin autoritativa (canal confiable) y, si la sesión ya
// consolidó, reescribe report.json para incluirlas.
func (s *Session) SetClientStats(raw json.RawMessage) {
	var claim struct {
		FramesSent int `json:"framesSent"`
	}
	_ = json.Unmarshal(raw, &claim)
	s.Reasm.MarkEnd(claim.FramesSent)

	s.mu.Lock()
	s.clientStats = raw
	alreadyDone := s.state == "done"
	s.mu.Unlock()

	if alreadyDone {
		s.persistReport()
	}
}

// finalizeOnce consolida la sesión: cierra parciales, persiste report.json y
// marca done. Seguro de llamar más de una vez.
func (s *Session) finalizeOnce() {
	rep := s.Reasm.Finalize()

	s.mu.Lock()
	if s.state == "done" {
		s.mu.Unlock()
		return
	}
	s.state = "done"
	s.report = &rep
	s.mu.Unlock()

	s.persistReport()
	close(s.done)
	s.log.Info("session finalized", "sessionId", s.ID,
		"framesComplete", rep.FramesComplete, "framesPartial", rep.FramesPartial,
		"framesLost", rep.FramesLost, "chunks", rep.ChunksReceived)
}

func (s *Session) persistReport() {
	s.mu.Lock()
	var rep reassembler.Report
	if s.report != nil {
		rep = *s.report
	}
	doc := storage.FinalReport{
		SessionID:   s.ID,
		PresetID:    s.Preset.ID,
		Transport:   s.transport,
		Report:      rep,
		ClientStats: s.clientStats,
	}
	s.mu.Unlock()

	if err := s.Store.WriteReport(doc); err != nil {
		s.log.Error("write report failed", "sessionId", s.ID, "err", err)
	}
}

// sweepLoop expira frames y dispara la consolidación. Corre en su propio
// goroutine desde la creación de la sesión hasta done.
func (s *Session) sweepLoop(every time.Duration, clock func() time.Time) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			now := clock()
			s.Reasm.Sweep(now)
			if s.Reasm.ShouldFinalize(now) {
				s.finalizeOnce()
				return
			}
		}
	}
}
