// Package storage persiste frames reconstruidos y el report de la sesión en
// disco (DATA_DIR/frames/<sessionId>/, PRD §4). Sin base de datos externa.
package storage

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"face-capture-poc/backend/internal/experiments"
	"face-capture-poc/backend/internal/reassembler"
)

// SessionStore implementa reassembler.FrameSink para una sesión.
type SessionStore struct {
	dir        string // DATA_DIR/frames/<sessionId>
	preset     experiments.Preset
	ffmpegPath string // "" si ffmpeg no está disponible (E8 no se decodifica)
	log        *slog.Logger

	mu    sync.Mutex
	saved map[uint16]savedFrame
	// rawH264 persistidos sin decodificar (decodeSkipped en el report)
	skippedDecode bool
}

type savedFrame struct {
	file  string // nombre dentro de dir, ej "12.jpg"
	bytes int
}

// FrameInfo alimenta GET /sessions/:id/frames.
type FrameInfo struct {
	FrameID uint16 `json:"frameId"`
	State   string `json:"state"` // "complete"
	File    string `json:"file"`
	Bytes   int    `json:"bytes"`
}

func NewSessionStore(dataDir, sessionID string, preset experiments.Preset, ffmpegPath string, log *slog.Logger) (*SessionStore, error) {
	if log == nil {
		log = slog.Default()
	}
	dir := filepath.Join(dataDir, "frames", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: mkdir %s: %w", dir, err)
	}
	return &SessionStore{dir: dir, preset: preset, ffmpegPath: ffmpegPath, log: log, saved: make(map[uint16]savedFrame)}, nil
}

// OnFrameComplete valida y persiste un frame reconstruido. Un error indica
// frame completo pero corrupto (cuenta como decodeError, PRD §7).
func (s *SessionStore) OnFrameComplete(f reassembler.CompleteFrame) error {
	file, n, err := s.decodeAndWrite(f)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.saved[f.FrameID] = savedFrame{file: file, bytes: n}
	s.mu.Unlock()
	return nil
}

// List devuelve los frames completos ordenados por frameId.
func (s *SessionStore) List() []FrameInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FrameInfo, 0, len(s.saved))
	for id, sf := range s.saved {
		out = append(out, FrameInfo{FrameID: id, State: "complete", File: sf.file, Bytes: sf.bytes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FrameID < out[j].FrameID })
	return out
}

// FramePath resuelve la ruta en disco de un archivo de frame ya validado.
func (s *SessionStore) FramePath(file string) string {
	return filepath.Join(s.dir, filepath.Base(file))
}

// DecodeSkipped indica si quedaron frames H.264 sin decodificar (sin ffmpeg).
func (s *SessionStore) DecodeSkipped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skippedDecode
}

// FinalReport es el documento completo persistido como report.json.
type FinalReport struct {
	SessionID     string             `json:"sessionId"`
	PresetID      string             `json:"presetId"`
	Transport     string             `json:"transport"`
	Report        reassembler.Report `json:"report"`
	DecodeSkipped bool               `json:"decodeSkipped"`
	ClientStats   json.RawMessage    `json:"clientStats,omitempty"`
}

// WriteReport persiste report.json (puede reescribirse si llegan client-stats
// después de la consolidación).
func (s *SessionStore) WriteReport(rep FinalReport) error {
	rep.DecodeSkipped = s.DecodeSkipped()
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("storage: marshal report: %w", err)
	}
	path := filepath.Join(s.dir, "report.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("storage: write report: %w", err)
	}
	return nil
}

// ReadReport lee el report.json persistido (para GET /sessions/:id/report.json).
func (s *SessionStore) ReadReport() ([]byte, error) {
	return os.ReadFile(filepath.Join(s.dir, "report.json"))
}
