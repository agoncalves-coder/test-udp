package protocol_test

import (
	"bytes"
	"errors"
	"testing"

	"face-capture-poc/backend/internal/protocol"
)

// FuzzDecodeChunk asserts two properties over arbitrary input bytes:
//  1. DecodeChunk never panics; on failure it returns one of the four
//     sentinel errors.
//  2. If decoding succeeds, EncodeChunk(header, payload) reproduces the
//     exact input bytes (lossless round-trip).
func FuzzDecodeChunk(f *testing.F) {
	// Seed corpus: the golden vectors (valid and invalid) plus edge cases.
	g := loadGolden(f)
	for _, v := range g.Valid {
		f.Add(mustHex(f, v.Hex))
	}
	for _, v := range g.Invalid {
		f.Add(mustHex(f, v.Hex))
	}
	f.Add([]byte{})
	f.Add([]byte{0xfa})
	f.Add([]byte{0xfa, 0xce})
	f.Add(make([]byte, protocol.HeaderSize))
	f.Add(make([]byte, protocol.MaxChunkSize+1))

	f.Fuzz(func(t *testing.T, b []byte) {
		h, payload, err := protocol.DecodeChunk(b)
		if err != nil {
			if !errors.Is(err, protocol.ErrBadMagic) &&
				!errors.Is(err, protocol.ErrShortHeader) &&
				!errors.Is(err, protocol.ErrLengthMismatch) &&
				!errors.Is(err, protocol.ErrInvalidHeader) {
				t.Fatalf("DecodeChunk returned a non-sentinel error: %v", err)
			}
			return
		}
		out, err := protocol.EncodeChunk(h, payload)
		if err != nil {
			t.Fatalf("EncodeChunk failed on a header DecodeChunk accepted: %v (header %+v)", err, h)
		}
		if !bytes.Equal(out, b) {
			t.Fatalf("round-trip mismatch:\n  in %x\n out %x", b, out)
		}
	})
}
