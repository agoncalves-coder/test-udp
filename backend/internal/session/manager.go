package session

import (
	"crypto/rand"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"face-capture-poc/backend/internal/experiments"
	"face-capture-poc/backend/internal/reassembler"
	"face-capture-poc/backend/internal/storage"
)

type ManagerConfig struct {
	DataDir    string
	FFmpegPath string
	Reasm      reassembler.Config
	SweepEvery time.Duration // ticker de expiración/consolidación por sesión
	TTL        time.Duration // evicción de sesiones terminadas o abandonadas
	Clock      func() time.Time
}

func DefaultManagerConfig(dataDir, ffmpegPath string) ManagerConfig {
	return ManagerConfig{
		DataDir:    dataDir,
		FFmpegPath: ffmpegPath,
		Reasm:      reassembler.DefaultConfig(),
		SweepEvery: 250 * time.Millisecond,
		TTL:        10 * time.Minute,
		Clock:      time.Now,
	}
}

// Manager crea y resuelve sesiones. El hot path de ingest NO pasa por acá:
// cada conexión queda ligada a su *Session en el setup.
type Manager struct {
	cfg ManagerConfig
	log *slog.Logger

	mu      sync.RWMutex
	byID    map[string]*Session
	bySeq   map[uint16]*Session
	nextSeq uint16
}

func NewManager(cfg ManagerConfig, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	m := &Manager{cfg: cfg, log: log, byID: make(map[string]*Session), bySeq: make(map[uint16]*Session)}
	go m.evictLoop()
	return m
}

func (m *Manager) Create(preset experiments.Preset) (*Session, error) {
	id, err := randomID()
	if err != nil {
		return nil, err
	}

	store, err := storage.NewSessionStore(m.cfg.DataDir, id, preset, m.cfg.FFmpegPath, m.log)
	if err != nil {
		return nil, err
	}

	s := &Session{
		ID:        id,
		Preset:    preset,
		CreatedAt: m.cfg.Clock(),
		Store:     store,
		log:       m.log,
		state:     "open",
		done:      make(chan struct{}),
	}
	s.Reasm = reassembler.New(m.cfg.Reasm, store, m.log.With("sessionId", id), m.cfg.Clock)

	m.mu.Lock()
	// Asignación monotónica de seq uint16 con wrap, salteando 0 y seqs vivos.
	for {
		m.nextSeq++
		if m.nextSeq == 0 {
			m.nextSeq = 1
		}
		if _, taken := m.bySeq[m.nextSeq]; !taken {
			break
		}
	}
	s.Seq = m.nextSeq
	m.byID[id] = s
	m.bySeq[s.Seq] = s
	m.mu.Unlock()

	go s.sweepLoop(m.cfg.SweepEvery, m.cfg.Clock)

	m.log.Info("session created", "sessionId", id, "sessionSeq", s.Seq, "preset", preset.ID)
	return s, nil
}

func (m *Manager) ByID(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byID[id]
	return s, ok
}

// evictLoop libera sesiones done (o abandonadas sin tráfico) pasado el TTL.
// Los frames en disco quedan; solo se libera la memoria.
func (m *Manager) evictLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := m.cfg.Clock()
		m.mu.Lock()
		for id, s := range m.byID {
			st := s.State()
			expired := now.Sub(s.CreatedAt) > m.cfg.TTL
			if (st == "done" && expired) || (st == "open" && expired) {
				delete(m.byID, id)
				delete(m.bySeq, s.Seq)
				m.log.Info("session evicted", "sessionId", id, "state", st)
			}
		}
		m.mu.Unlock()
	}
}

func randomID() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: random id: %w", err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}
