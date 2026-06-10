// Package protocol implements the 12-byte big-endian binary chunk header
// defined in PRD §4. The wire format is contract-tested on both sides
// (Go and TypeScript) against shared/protocol-golden.json.
//
// Layout (12 bytes, big-endian) + payload (max 1100 bytes):
//
//	offset  size  field
//	0       2     magic        (0xFACE)
//	2       2     sessionSeq   (uint16)
//	4       2     frameId      (uint16)
//	6       1     chunkIndex   (uint8)
//	7       1     totalChunks  (uint8)
//	8       2     payloadLen   (uint16)
//	10      1     flags        (1=grayscale 2=keyframe 4=lastFrame)
//	11      1     codec        (0=JPEG 1=WebP 2=raw-gray 3=h264-annexb)
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Magic is the constant 2-byte prefix of every chunk (0xFA 0xCE).
	Magic uint16 = 0xFACE
	// HeaderSize is the fixed header length in bytes.
	HeaderSize = 12
	// MaxPayload is the maximum payload size per chunk in bytes.
	MaxPayload = 1100
	// MaxChunkSize is the maximum total chunk size (header + payload).
	MaxChunkSize = HeaderSize + MaxPayload // 1112
)

// Codec identifies the frame encoding carried in the payload.
type Codec uint8

const (
	CodecJPEG    Codec = 0
	CodecWebP    Codec = 1
	CodecRawGray Codec = 2
	CodecH264    Codec = 3

	maxCodec = CodecH264
)

// Flags is the bitset at header offset 10.
type Flags uint8

const (
	FlagGrayscale Flags = 1 << 0
	FlagKeyframe  Flags = 1 << 1
	FlagLastFrame Flags = 1 << 2
)

// Sentinel errors, matched with errors.Is. The names mirror the error
// identifiers used in shared/protocol-golden.json.
var (
	// ErrBadMagic (BAD_MAGIC): the first two bytes are not 0xFACE.
	ErrBadMagic = errors.New("protocol: bad magic")
	// ErrShortHeader (SHORT_HEADER): fewer than HeaderSize bytes.
	ErrShortHeader = errors.New("protocol: short header")
	// ErrLengthMismatch (LENGTH_MISMATCH): total length != HeaderSize+payloadLen.
	ErrLengthMismatch = errors.New("protocol: length mismatch")
	// ErrInvalidHeader (INVALID_HEADER): field out of range
	// (totalChunks==0, chunkIndex>=totalChunks, payloadLen>MaxPayload, codec>3).
	ErrInvalidHeader = errors.New("protocol: invalid header")
)

// ChunkHeader is the decoded 12-byte chunk header.
type ChunkHeader struct {
	SessionSeq  uint16
	FrameID     uint16
	ChunkIndex  uint8
	TotalChunks uint8
	PayloadLen  uint16
	Flags       Flags
	Codec       Codec
}

// validate enforces the field invariants shared by encode and decode.
func (h ChunkHeader) validate() error {
	if h.TotalChunks < 1 {
		return fmt.Errorf("%w: totalChunks must be >= 1", ErrInvalidHeader)
	}
	if h.ChunkIndex >= h.TotalChunks {
		return fmt.Errorf("%w: chunkIndex %d >= totalChunks %d", ErrInvalidHeader, h.ChunkIndex, h.TotalChunks)
	}
	if h.PayloadLen > MaxPayload {
		return fmt.Errorf("%w: payloadLen %d > max %d", ErrInvalidHeader, h.PayloadLen, MaxPayload)
	}
	if h.Codec > maxCodec {
		return fmt.Errorf("%w: codec %d > max %d", ErrInvalidHeader, h.Codec, maxCodec)
	}
	return nil
}

// EncodeChunk serializes the header and payload into a single wire chunk.
// h.PayloadLen must equal len(payload) (ErrLengthMismatch otherwise) and all
// header invariants are validated (ErrInvalidHeader).
func EncodeChunk(h ChunkHeader, payload []byte) ([]byte, error) {
	if int(h.PayloadLen) != len(payload) {
		return nil, fmt.Errorf("%w: payloadLen %d != len(payload) %d", ErrLengthMismatch, h.PayloadLen, len(payload))
	}
	if err := h.validate(); err != nil {
		return nil, err
	}
	b := make([]byte, HeaderSize+len(payload))
	binary.BigEndian.PutUint16(b[0:2], Magic)
	binary.BigEndian.PutUint16(b[2:4], h.SessionSeq)
	binary.BigEndian.PutUint16(b[4:6], h.FrameID)
	b[6] = h.ChunkIndex
	b[7] = h.TotalChunks
	binary.BigEndian.PutUint16(b[8:10], h.PayloadLen)
	b[10] = byte(h.Flags)
	b[11] = byte(h.Codec)
	copy(b[HeaderSize:], payload)
	return b, nil
}

// DecodeChunk parses a wire chunk. The returned payload is a zero-copy
// subslice of b: it aliases b's backing array and is only valid while b is.
// Validation order: ErrShortHeader, ErrBadMagic, ErrLengthMismatch
// (len(b) must be exactly HeaderSize+payloadLen), then ErrInvalidHeader.
func DecodeChunk(b []byte) (ChunkHeader, []byte, error) {
	if len(b) < HeaderSize {
		return ChunkHeader{}, nil, fmt.Errorf("%w: got %d bytes, need %d", ErrShortHeader, len(b), HeaderSize)
	}
	if m := binary.BigEndian.Uint16(b[0:2]); m != Magic {
		return ChunkHeader{}, nil, fmt.Errorf("%w: 0x%04x", ErrBadMagic, m)
	}
	h := ChunkHeader{
		SessionSeq:  binary.BigEndian.Uint16(b[2:4]),
		FrameID:     binary.BigEndian.Uint16(b[4:6]),
		ChunkIndex:  b[6],
		TotalChunks: b[7],
		PayloadLen:  binary.BigEndian.Uint16(b[8:10]),
		Flags:       Flags(b[10]),
		Codec:       Codec(b[11]),
	}
	if len(b) != HeaderSize+int(h.PayloadLen) {
		return ChunkHeader{}, nil, fmt.Errorf("%w: got %d bytes, header declares %d", ErrLengthMismatch, len(b), HeaderSize+int(h.PayloadLen))
	}
	if err := h.validate(); err != nil {
		return ChunkHeader{}, nil, err
	}
	return h, b[HeaderSize:], nil
}
