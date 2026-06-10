package protocol_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"face-capture-poc/backend/internal/protocol"
)

// goldenPath points at the shared contract file consumed by both the Go and
// the TypeScript test suites. If either side drifts, its tests fail.
const goldenPath = "../../../shared/protocol-golden.json"

type goldenFields struct {
	SessionSeq  uint16 `json:"sessionSeq"`
	FrameID     uint16 `json:"frameId"`
	ChunkIndex  uint8  `json:"chunkIndex"`
	TotalChunks uint8  `json:"totalChunks"`
	PayloadLen  uint16 `json:"payloadLen"`
	Flags       uint8  `json:"flags"`
	Codec       uint8  `json:"codec"`
}

type goldenValid struct {
	Name       string       `json:"name"`
	Hex        string       `json:"hex"`
	Fields     goldenFields `json:"fields"`
	PayloadHex string       `json:"payloadHex"`
}

type goldenInvalid struct {
	Name  string `json:"name"`
	Hex   string `json:"hex"`
	Error string `json:"error"`
}

type goldenFile struct {
	Valid   []goldenValid   `json:"valid"`
	Invalid []goldenInvalid `json:"invalid"`
}

var errorByName = map[string]error{
	"BAD_MAGIC":       protocol.ErrBadMagic,
	"SHORT_HEADER":    protocol.ErrShortHeader,
	"LENGTH_MISMATCH": protocol.ErrLengthMismatch,
	"INVALID_HEADER":  protocol.ErrInvalidHeader,
}

func loadGolden(tb testing.TB) goldenFile {
	tb.Helper()
	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		tb.Fatalf("reading golden vectors: %v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		tb.Fatalf("parsing golden vectors: %v", err)
	}
	if len(g.Valid) == 0 || len(g.Invalid) == 0 {
		tb.Fatalf("golden file has %d valid / %d invalid vectors; expected both non-empty", len(g.Valid), len(g.Invalid))
	}
	return g
}

func mustHex(tb testing.TB, s string) []byte {
	tb.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		tb.Fatalf("invalid hex %q in golden file: %v", s, err)
	}
	return b
}

func headerFromFields(f goldenFields) protocol.ChunkHeader {
	return protocol.ChunkHeader{
		SessionSeq:  f.SessionSeq,
		FrameID:     f.FrameID,
		ChunkIndex:  f.ChunkIndex,
		TotalChunks: f.TotalChunks,
		PayloadLen:  f.PayloadLen,
		Flags:       protocol.Flags(f.Flags),
		Codec:       protocol.Codec(f.Codec),
	}
}

func TestGoldenValidDecode(t *testing.T) {
	g := loadGolden(t)
	for _, v := range g.Valid {
		t.Run(v.Name, func(t *testing.T) {
			raw := mustHex(t, v.Hex)
			wantPayload := mustHex(t, v.PayloadHex)

			h, payload, err := protocol.DecodeChunk(raw)
			if err != nil {
				t.Fatalf("DecodeChunk: unexpected error: %v", err)
			}
			if want := headerFromFields(v.Fields); h != want {
				t.Errorf("header mismatch:\n got %+v\nwant %+v", h, want)
			}
			if !bytes.Equal(payload, wantPayload) {
				t.Errorf("payload mismatch:\n got %x\nwant %x", payload, wantPayload)
			}
			// The payload must be a zero-copy subslice of the input.
			if len(payload) > 0 && &payload[0] != &raw[protocol.HeaderSize] {
				t.Error("payload is not a zero-copy subslice of the input buffer")
			}
		})
	}
}

func TestGoldenValidEncode(t *testing.T) {
	g := loadGolden(t)
	for _, v := range g.Valid {
		t.Run(v.Name, func(t *testing.T) {
			want := mustHex(t, v.Hex)
			payload := mustHex(t, v.PayloadHex)

			got, err := protocol.EncodeChunk(headerFromFields(v.Fields), payload)
			if err != nil {
				t.Fatalf("EncodeChunk: unexpected error: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("encoded bytes mismatch:\n got %x\nwant %x", got, want)
			}
		})
	}
}

func TestGoldenInvalidDecode(t *testing.T) {
	g := loadGolden(t)
	for _, v := range g.Invalid {
		t.Run(v.Name, func(t *testing.T) {
			want, ok := errorByName[v.Error]
			if !ok {
				t.Fatalf("golden file references unknown error name %q", v.Error)
			}
			_, _, err := protocol.DecodeChunk(mustHex(t, v.Hex))
			if err == nil {
				t.Fatalf("DecodeChunk: expected %v, got nil", want)
			}
			if !errors.Is(err, want) {
				t.Errorf("DecodeChunk error = %v; want errors.Is(err, %v)", err, want)
			}
		})
	}
}

// Encode must enforce the same invariants as decode.
func TestEncodeChunkValidation(t *testing.T) {
	valid := protocol.ChunkHeader{
		SessionSeq:  1,
		FrameID:     1,
		ChunkIndex:  0,
		TotalChunks: 1,
		PayloadLen:  3,
		Flags:       0,
		Codec:       protocol.CodecJPEG,
	}
	payload := []byte{0x61, 0x62, 0x63}

	cases := []struct {
		name    string
		mutate  func(h *protocol.ChunkHeader, p *[]byte)
		wantErr error
	}{
		{
			name:    "payloadLen-vs-payload-mismatch",
			mutate:  func(h *protocol.ChunkHeader, p *[]byte) { h.PayloadLen = 99 },
			wantErr: protocol.ErrLengthMismatch,
		},
		{
			name:    "zero-total-chunks",
			mutate:  func(h *protocol.ChunkHeader, p *[]byte) { h.TotalChunks = 0 },
			wantErr: protocol.ErrInvalidHeader,
		},
		{
			name: "index-ge-total",
			mutate: func(h *protocol.ChunkHeader, p *[]byte) {
				h.ChunkIndex = 2
				h.TotalChunks = 2
			},
			wantErr: protocol.ErrInvalidHeader,
		},
		{
			name: "payload-too-large",
			mutate: func(h *protocol.ChunkHeader, p *[]byte) {
				*p = make([]byte, protocol.MaxPayload+1)
				h.PayloadLen = protocol.MaxPayload + 1
			},
			wantErr: protocol.ErrInvalidHeader,
		},
		{
			name:    "codec-out-of-range",
			mutate:  func(h *protocol.ChunkHeader, p *[]byte) { h.Codec = 4 },
			wantErr: protocol.ErrInvalidHeader,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := valid
			p := append([]byte(nil), payload...)
			tc.mutate(&h, &p)
			if _, err := protocol.EncodeChunk(h, p); !errors.Is(err, tc.wantErr) {
				t.Errorf("EncodeChunk error = %v; want errors.Is(err, %v)", err, tc.wantErr)
			}
		})
	}

	// Sanity: the unmutated header encodes without error.
	if _, err := protocol.EncodeChunk(valid, payload); err != nil {
		t.Errorf("EncodeChunk on valid input: unexpected error: %v", err)
	}
}

func TestMaxPayloadBoundary(t *testing.T) {
	payload := make([]byte, protocol.MaxPayload)
	h := protocol.ChunkHeader{
		TotalChunks: 1,
		PayloadLen:  protocol.MaxPayload,
		Codec:       protocol.CodecJPEG,
	}
	b, err := protocol.EncodeChunk(h, payload)
	if err != nil {
		t.Fatalf("EncodeChunk at MaxPayload: %v", err)
	}
	if len(b) != protocol.MaxChunkSize {
		t.Fatalf("chunk size = %d; want MaxChunkSize %d", len(b), protocol.MaxChunkSize)
	}
	h2, p2, err := protocol.DecodeChunk(b)
	if err != nil {
		t.Fatalf("DecodeChunk at MaxChunkSize: %v", err)
	}
	if h2 != h {
		t.Errorf("round-trip header mismatch:\n got %+v\nwant %+v", h2, h)
	}
	if !bytes.Equal(p2, payload) {
		t.Error("round-trip payload mismatch at MaxPayload")
	}
}
