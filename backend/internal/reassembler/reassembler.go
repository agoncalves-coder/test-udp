// Package reassembler reconstruye frames a partir de chunks que llegan por un
// canal no confiable: sin orden, con pérdida y con duplicados (PRD §4).
// Una instancia por sesión; los callers concurrentes son el read-loop del
// transporte y el ticker de sweep de la sesión.
package reassembler

import (
	"log/slog"
	"sync"
	"time"

	"face-capture-poc/backend/internal/protocol"
)

type Config struct {
	FrameTimeout time.Duration // desde el primer chunk de un frame (PRD: 1500ms)
	EndGrace     time.Duration // espera tras END_OF_CAPTURE por chunks rezagados (PRD: 2s)
	IdleTimeout  time.Duration // sin chunks ni END: finalizar igual
}

func DefaultConfig() Config {
	return Config{FrameTimeout: 1500 * time.Millisecond, EndGrace: 2 * time.Second, IdleTimeout: 5 * time.Second}
}

// CompleteFrame es un frame reconstruido entregado al sink exactamente una vez.
type CompleteFrame struct {
	FrameID      uint16
	Codec        protocol.Codec
	Flags        protocol.Flags
	Data         []byte
	FirstChunkAt time.Time
	CompletedAt  time.Time
}

// FrameSink persiste/valida frames completos. Un error cuenta como error de
// decodificación (frame completo pero corrupto, PRD §7), no detiene la sesión.
type FrameSink interface {
	OnFrameComplete(f CompleteFrame) error
}

const (
	statePending = iota
	stateComplete
	stateTimedOut
)

type frameState struct {
	status       int
	chunks       [][]byte // nil tras COMPLETE/TIMED_OUT (buffers liberados)
	received     int
	total        int
	codec        protocol.Codec
	flags        protocol.Flags
	firstChunkAt time.Time
	receivedPct  float64 // fijado al expirar
}

type Reassembler struct {
	mu    sync.Mutex
	cfg   Config
	sink  FrameSink
	log   *slog.Logger
	clock func() time.Time

	frames map[uint16]*frameState

	chunksReceived     int
	chunksDuplicate    int
	chunksLate         int
	chunksWrongSession int
	protocolErrors     int
	decodeErrors       int
	bytesReceived      int64

	firstChunkAt    time.Time
	lastChunkAt     time.Time
	lastCompletedAt time.Time
	latencies       []time.Duration

	endMarked       bool
	endAt           time.Time
	framesSentClaim int
	closedAt        time.Time // transporte cerrado sin END explícito
	finalized       bool
	finalReport     Report
}

func New(cfg Config, sink FrameSink, log *slog.Logger, clock func() time.Time) *Reassembler {
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reassembler{cfg: cfg, sink: sink, log: log, clock: clock, frames: make(map[uint16]*frameState)}
}

// Ingest procesa un chunk ya decodificado y validado contra la sesión.
// Copia el payload (el buffer del transporte puede reutilizarse). Si el chunk
// completa un frame, el sink se invoca fuera del mutex.
func (r *Reassembler) Ingest(h protocol.ChunkHeader, payload []byte) {
	now := r.clock()

	r.mu.Lock()
	if r.finalized {
		r.chunksLate++
		r.mu.Unlock()
		return
	}
	r.chunksReceived++
	r.bytesReceived += int64(len(payload))
	if r.firstChunkAt.IsZero() {
		r.firstChunkAt = now
	}
	r.lastChunkAt = now

	fs, ok := r.frames[h.FrameID]
	if !ok {
		fs = &frameState{
			status:       statePending,
			chunks:       make([][]byte, h.TotalChunks),
			total:        int(h.TotalChunks),
			codec:        h.Codec,
			flags:        h.Flags,
			firstChunkAt: now,
		}
		r.frames[h.FrameID] = fs
	}

	if fs.status != statePending {
		r.chunksLate++
		r.mu.Unlock()
		return
	}
	if int(h.TotalChunks) != fs.total || h.Codec != fs.codec {
		r.protocolErrors++
		r.mu.Unlock()
		return
	}
	if fs.chunks[h.ChunkIndex] != nil {
		r.chunksDuplicate++
		r.mu.Unlock()
		return
	}

	cp := make([]byte, len(payload))
	copy(cp, payload)
	fs.chunks[h.ChunkIndex] = cp
	fs.received++

	var complete *CompleteFrame
	if fs.received == fs.total {
		size := 0
		for _, c := range fs.chunks {
			size += len(c)
		}
		data := make([]byte, 0, size)
		for _, c := range fs.chunks {
			data = append(data, c...)
		}
		fs.status = stateComplete
		fs.chunks = nil
		r.latencies = append(r.latencies, now.Sub(fs.firstChunkAt))
		r.lastCompletedAt = now
		complete = &CompleteFrame{
			FrameID:      h.FrameID,
			Codec:        fs.codec,
			Flags:        fs.flags,
			Data:         data,
			FirstChunkAt: fs.firstChunkAt,
			CompletedAt:  now,
		}
	}
	r.mu.Unlock()

	if complete != nil && r.sink != nil {
		if err := r.sink.OnFrameComplete(*complete); err != nil {
			r.log.Warn("frame decode failed", "frameId", complete.FrameID, "err", err)
			r.mu.Lock()
			r.decodeErrors++
			r.mu.Unlock()
		}
	}
}

// NoteWrongSession registra un chunk cuyo sessionSeq no corresponde a la
// sesión ligada a la conexión (se descarta en el transporte).
func (r *Reassembler) NoteWrongSession() {
	r.mu.Lock()
	r.chunksWrongSession++
	r.mu.Unlock()
}

// NoteProtocolError registra un datagrama que no decodifica como chunk válido.
func (r *Reassembler) NoteProtocolError() {
	r.mu.Lock()
	r.protocolErrors++
	r.mu.Unlock()
}

// Sweep expira frames PENDING cuyo primer chunk superó FrameTimeout.
func (r *Reassembler) Sweep(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked(now)
}

func (r *Reassembler) sweepLocked(now time.Time) {
	for id, fs := range r.frames {
		if fs.status == statePending && now.Sub(fs.firstChunkAt) > r.cfg.FrameTimeout {
			fs.status = stateTimedOut
			fs.receivedPct = 100 * float64(fs.received) / float64(fs.total)
			fs.chunks = nil
			r.log.Debug("frame timed out", "frameId", id, "receivedPct", fs.receivedPct)
		}
	}
}

// MarkEnd registra END_OF_CAPTURE. Idempotente: la primera llamada con
// framesSent > 0 fija el claim; llamadas posteriores no lo pisan.
func (r *Reassembler) MarkEnd(framesSent int) {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.framesSentClaim == 0 && framesSent > 0 {
		r.framesSentClaim = framesSent
	}
	if r.endMarked {
		return
	}
	r.endMarked = true
	r.endAt = now
}

// NoteTransportClosed: el canal se cerró; si no hubo END explícito, arranca
// la cuenta regresiva de finalización igual (el cliente pudo morir).
func (r *Reassembler) NoteTransportClosed() {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closedAt.IsZero() {
		r.closedAt = now
	}
}

// ShouldFinalize decide si la sesión debe consolidarse:
// END + EndGrace transcurrida, cierre de transporte + EndGrace, o
// IdleTimeout sin chunks después de haber recibido al menos uno.
func (r *Reassembler) ShouldFinalize(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalized {
		return false
	}
	if r.endMarked && now.Sub(r.endAt) >= r.cfg.EndGrace {
		return true
	}
	if !r.closedAt.IsZero() && now.Sub(r.closedAt) >= r.cfg.EndGrace {
		return true
	}
	if !r.lastChunkAt.IsZero() && now.Sub(r.lastChunkAt) >= r.cfg.IdleTimeout {
		return true
	}
	return false
}

// Finalize cierra los frames pendientes como parciales y consolida el Report.
// Idempotente: llamadas posteriores devuelven el mismo Report.
func (r *Reassembler) Finalize() Report {
	now := r.clock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finalized {
		return r.finalReport
	}
	// Pendientes al cierre → parciales (no llegó el resto y ya no llegará).
	for _, fs := range r.frames {
		if fs.status == statePending {
			fs.status = stateTimedOut
			fs.receivedPct = 100 * float64(fs.received) / float64(fs.total)
			fs.chunks = nil
		}
	}
	r.finalReport = r.buildReportLocked(now)
	r.finalized = true
	return r.finalReport
}

// LiveStats expone contadores mínimos para polling durante la captura.
type LiveStats struct {
	FramesComplete int   `json:"framesComplete"`
	FramesPending  int   `json:"framesPending"`
	ChunksReceived int   `json:"chunksReceived"`
	BytesReceived  int64 `json:"bytesReceived"`
}

func (r *Reassembler) Live() LiveStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := LiveStats{ChunksReceived: r.chunksReceived, BytesReceived: r.bytesReceived}
	for _, fs := range r.frames {
		switch fs.status {
		case stateComplete:
			s.FramesComplete++
		case statePending:
			s.FramesPending++
		}
	}
	return s
}
