package reassembler

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"face-capture-poc/backend/internal/protocol"
)

// fakeClock avanza manualmente; el reassembler nunca llama time.Now.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// collectSink acumula frames completos; opcionalmente devuelve error (decode fallido).
type collectSink struct {
	mu     sync.Mutex
	frames []CompleteFrame
	fail   bool
}

func (s *collectSink) OnFrameComplete(f CompleteFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return fmt.Errorf("decode fallido simulado")
	}
	s.frames = append(s.frames, f)
	return nil
}

func (s *collectSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func testCfg() Config {
	return Config{FrameTimeout: 1500 * time.Millisecond, EndGrace: 2 * time.Second, IdleTimeout: 5 * time.Second}
}

func header(frameID uint16, idx, total uint8, payloadLen int) protocol.ChunkHeader {
	return protocol.ChunkHeader{
		SessionSeq:  1,
		FrameID:     frameID,
		ChunkIndex:  idx,
		TotalChunks: total,
		PayloadLen:  uint16(payloadLen),
		Codec:       protocol.CodecJPEG,
	}
}

func TestCompleteInOrder(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	parts := [][]byte{[]byte("aaa"), []byte("bbb"), []byte("cc")}
	for i, p := range parts {
		r.Ingest(header(0, uint8(i), 3, len(p)), p)
		clk.Advance(10 * time.Millisecond)
	}

	if sink.count() != 1 {
		t.Fatalf("sink llamado %d veces, esperaba 1", sink.count())
	}
	got := sink.frames[0]
	if !bytes.Equal(got.Data, []byte("aaabbbcc")) {
		t.Errorf("data = %q, esperaba aaabbbcc", got.Data)
	}
	if got.CompletedAt.Sub(got.FirstChunkAt) != 20*time.Millisecond {
		t.Errorf("latencia = %v, esperaba 20ms", got.CompletedAt.Sub(got.FirstChunkAt))
	}
}

func TestCompleteOutOfOrder(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	r.Ingest(header(0, 2, 3, 2), []byte("cc"))
	r.Ingest(header(0, 0, 3, 3), []byte("aaa"))
	r.Ingest(header(0, 1, 3, 3), []byte("bbb"))

	if sink.count() != 1 {
		t.Fatalf("sink llamado %d veces, esperaba 1", sink.count())
	}
	if !bytes.Equal(sink.frames[0].Data, []byte("aaabbbcc")) {
		t.Errorf("data = %q: el orden de llegada no debe afectar la concatenación", sink.frames[0].Data)
	}
}

func TestDuplicate(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	r.Ingest(header(0, 0, 2, 1), []byte("a"))
	r.Ingest(header(0, 0, 2, 1), []byte("a")) // duplicado
	r.Ingest(header(0, 1, 2, 1), []byte("b"))

	rep := r.Finalize()
	if rep.ChunksDuplicate != 1 {
		t.Errorf("chunksDuplicate = %d, esperaba 1", rep.ChunksDuplicate)
	}
	if sink.count() != 1 {
		t.Errorf("sink llamado %d veces, esperaba exactamente 1", sink.count())
	}
}

func TestTimeoutPartial(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	r.Ingest(header(0, 0, 3, 1), []byte("a"))
	r.Ingest(header(0, 1, 3, 1), []byte("b")) // 2 de 3
	clk.Advance(1501 * time.Millisecond)
	r.Sweep(clk.Now())

	if sink.count() != 0 {
		t.Fatalf("sink no debe llamarse para frames parciales")
	}
	fs := r.frames[0]
	if fs.status != stateTimedOut {
		t.Fatalf("status = %d, esperaba TIMED_OUT", fs.status)
	}
	if fs.chunks != nil {
		t.Error("los buffers del frame expirado deben liberarse (chunks nil)")
	}
	if pct := fs.receivedPct; pct < 66.6 || pct > 66.7 {
		t.Errorf("receivedPct = %.2f, esperaba ~66.67", pct)
	}
}

func TestLateChunk(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	// Tras COMPLETE.
	r.Ingest(header(0, 0, 1, 1), []byte("a"))
	r.Ingest(header(0, 0, 1, 1), []byte("a"))
	// Tras TIMED_OUT.
	r.Ingest(header(1, 0, 2, 1), []byte("x"))
	clk.Advance(1501 * time.Millisecond)
	r.Sweep(clk.Now())
	r.Ingest(header(1, 1, 2, 1), []byte("y"))

	rep := r.Finalize()
	if rep.ChunksLate != 2 {
		t.Errorf("chunksLate = %d, esperaba 2 (uno tras COMPLETE, otro tras TIMED_OUT)", rep.ChunksLate)
	}
	if sink.count() != 1 {
		t.Errorf("sink llamado %d veces, esperaba 1", sink.count())
	}
}

func TestMismatchedTotal(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	r.Ingest(header(0, 0, 3, 1), []byte("a"))
	r.Ingest(header(0, 1, 4, 1), []byte("b")) // totalChunks inconsistente

	rep := r.Finalize()
	if rep.ProtocolErrors != 1 {
		t.Errorf("protocolErrors = %d, esperaba 1", rep.ProtocolErrors)
	}
	if sink.count() != 0 {
		t.Errorf("el chunk inconsistente no debe contar para completar el frame")
	}
}

func TestEndGraceAndFinalize(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	// Frame 0 completo con latencia 100ms; frame 1 completo con latencia 300ms.
	r.Ingest(header(0, 0, 2, 1), []byte("a"))
	clk.Advance(100 * time.Millisecond)
	r.Ingest(header(0, 1, 2, 1), []byte("b"))
	r.Ingest(header(1, 0, 2, 1), []byte("c"))
	clk.Advance(300 * time.Millisecond)

	r.MarkEnd(4)
	if r.ShouldFinalize(clk.Now()) {
		t.Fatal("no debe finalizar antes de EndGrace")
	}

	// Chunk rezagado dentro de la gracia: completa el frame 1.
	clk.Advance(500 * time.Millisecond)
	r.Ingest(header(1, 1, 2, 1), []byte("d"))
	if sink.count() != 2 {
		t.Fatalf("el chunk rezagado dentro de la gracia debe completar el frame")
	}

	clk.Advance(1501 * time.Millisecond) // 2001ms desde MarkEnd
	if !r.ShouldFinalize(clk.Now()) {
		t.Fatal("debe finalizar pasada EndGrace")
	}

	rep := r.Finalize()
	if rep.FramesExpected != 4 || rep.FramesComplete != 2 || rep.FramesPartial != 0 || rep.FramesLost != 2 {
		t.Errorf("expected/complete/partial/lost = %d/%d/%d/%d, esperaba 4/2/0/2",
			rep.FramesExpected, rep.FramesComplete, rep.FramesPartial, rep.FramesLost)
	}
	// Latencias: [100ms, 800ms] (frame 1: 300+500). p50 nearest-rank = 100ms, p95 = 800ms.
	if rep.LatencyP50Ms != 100 {
		t.Errorf("p50 = %v, esperaba 100", rep.LatencyP50Ms)
	}
	if rep.LatencyP95Ms != 800 {
		t.Errorf("p95 = %v, esperaba 800", rep.LatencyP95Ms)
	}

	// Finalize es idempotente.
	rep2 := r.Finalize()
	if rep2.FramesComplete != rep.FramesComplete || rep2.ChunksReceived != rep.ChunksReceived {
		t.Error("Finalize debe ser idempotente")
	}
	// MarkEnd posterior no pisa el claim original.
	r.MarkEnd(99)
	if r.finalReport.FramesSentByClient != 4 {
		t.Error("el claim de framesSent no debe pisarse")
	}
}

func TestIdleFinalize(t *testing.T) {
	clk := newFakeClock()
	r := New(testCfg(), &collectSink{}, nil, clk.Now)

	r.Ingest(header(0, 0, 1, 1), []byte("a"))
	clk.Advance(5001 * time.Millisecond)
	if !r.ShouldFinalize(clk.Now()) {
		t.Error("debe finalizar por idle sin END")
	}
}

func TestTransportClosedFinalize(t *testing.T) {
	clk := newFakeClock()
	r := New(testCfg(), &collectSink{}, nil, clk.Now)

	r.Ingest(header(0, 0, 1, 1), []byte("a"))
	r.NoteTransportClosed()
	clk.Advance(2001 * time.Millisecond)
	if !r.ShouldFinalize(clk.Now()) {
		t.Error("debe finalizar tras cierre de transporte + gracia")
	}
}

func TestDecodeErrorCounted(t *testing.T) {
	clk := newFakeClock()
	r := New(testCfg(), &collectSink{fail: true}, nil, clk.Now)

	r.Ingest(header(0, 0, 1, 1), []byte("a"))
	rep := r.Finalize()
	if rep.DecodeErrors != 1 {
		t.Errorf("decodeErrors = %d, esperaba 1", rep.DecodeErrors)
	}
	// El frame igual cuenta como completo (completo pero corrupto, PRD §7).
	if rep.FramesComplete != 1 {
		t.Errorf("framesComplete = %d, esperaba 1", rep.FramesComplete)
	}
}

func TestConcurrentIngest(t *testing.T) {
	clk := newFakeClock()
	sink := &collectSink{}
	r := New(testCfg(), sink, nil, clk.Now)

	const nFrames = 50
	var wg sync.WaitGroup
	for f := 0; f < nFrames; f++ {
		wg.Add(1)
		go func(frameID uint16) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				p := []byte{byte(frameID), byte(i)}
				r.Ingest(header(frameID, uint8(i), 3, len(p)), p)
			}
		}(uint16(f))
	}
	wg.Wait()

	if sink.count() != nFrames {
		t.Errorf("frames completos = %d, esperaba %d", sink.count(), nFrames)
	}
	rep := r.Finalize()
	if rep.ChunksReceived != nFrames*3 {
		t.Errorf("chunksReceived = %d, esperaba %d", rep.ChunksReceived, nFrames*3)
	}
}
