package reassembler

import (
	"sort"
	"time"
)

// PartialFrame describe un frame que expiró incompleto y qué fracción llegó
// (dato valioso para el análisis, PRD §4).
type PartialFrame struct {
	FrameID     uint16  `json:"frameId"`
	ReceivedPct float64 `json:"receivedPct"`
}

// Report son las métricas por sesión del PRD §7.
type Report struct {
	FramesExpected int            `json:"framesExpected"`
	FramesComplete int            `json:"framesComplete"`
	FramesPartial  int            `json:"framesPartial"`
	FramesLost     int            `json:"framesLost"`
	Partials       []PartialFrame `json:"partials"`

	ChunksReceived     int `json:"chunksReceived"`
	ChunksDuplicate    int `json:"chunksDuplicate"`
	ChunksLate         int `json:"chunksLate"`
	ChunksWrongSession int `json:"chunksWrongSession"`
	ProtocolErrors     int `json:"protocolErrors"`
	DecodeErrors       int `json:"decodeErrors"`

	BytesReceived       int64 `json:"bytesReceived"`
	EffectiveBitrateBps int64 `json:"effectiveBitrateBps"`

	// Latencia primer-chunk → frame-completo.
	LatencyP50Ms float64 `json:"latencyP50Ms"`
	LatencyP95Ms float64 `json:"latencyP95Ms"`
	// TotalMs: primer chunk recibido → último frame consolidado.
	TotalMs float64 `json:"totalMs"`

	// FramesSentByClient: claim del END_OF_CAPTURE (0 si nunca llegó).
	FramesSentByClient int `json:"framesSentByClient"`
}

func (r *Reassembler) buildReportLocked(now time.Time) Report {
	rep := Report{
		ChunksReceived:     r.chunksReceived,
		ChunksDuplicate:    r.chunksDuplicate,
		ChunksLate:         r.chunksLate,
		ChunksWrongSession: r.chunksWrongSession,
		ProtocolErrors:     r.protocolErrors,
		DecodeErrors:       r.decodeErrors,
		BytesReceived:      r.bytesReceived,
		FramesSentByClient: r.framesSentClaim,
		Partials:           []PartialFrame{},
	}

	maxFrameID := -1
	for id, fs := range r.frames {
		if int(id) > maxFrameID {
			maxFrameID = int(id)
		}
		switch fs.status {
		case stateComplete:
			rep.FramesComplete++
		case stateTimedOut:
			rep.FramesPartial++
			rep.Partials = append(rep.Partials, PartialFrame{FrameID: id, ReceivedPct: fs.receivedPct})
		}
	}
	sort.Slice(rep.Partials, func(i, j int) bool { return rep.Partials[i].FrameID < rep.Partials[j].FrameID })

	// framesExpected: claim del cliente; fallback: mayor frameId visto + 1.
	rep.FramesExpected = r.framesSentClaim
	if rep.FramesExpected == 0 && maxFrameID >= 0 {
		rep.FramesExpected = maxFrameID + 1
	}
	if lost := rep.FramesExpected - rep.FramesComplete - rep.FramesPartial; lost > 0 {
		rep.FramesLost = lost
	}

	if !r.firstChunkAt.IsZero() && !r.lastCompletedAt.IsZero() {
		rep.TotalMs = float64(r.lastCompletedAt.Sub(r.firstChunkAt)) / float64(time.Millisecond)
	}
	if !r.firstChunkAt.IsZero() {
		span := r.lastChunkAt.Sub(r.firstChunkAt)
		if span > 0 {
			rep.EffectiveBitrateBps = int64(float64(r.bytesReceived*8) / span.Seconds())
		}
	}

	rep.LatencyP50Ms = percentileMs(r.latencies, 0.50)
	rep.LatencyP95Ms = percentileMs(r.latencies, 0.95)
	return rep
}

// percentileMs calcula el percentil por nearest-rank sobre una copia ordenada.
func percentileMs(lat []time.Duration, p float64) float64 {
	if len(lat) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(lat))
	copy(sorted, lat)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(float64(len(sorted))*p+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return float64(sorted[rank]) / float64(time.Millisecond)
}
