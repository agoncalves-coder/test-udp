package storage

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"

	"golang.org/x/image/webp"

	"face-capture-poc/backend/internal/protocol"
	"face-capture-poc/backend/internal/reassembler"
)

// jpegQuality para re-encodes server-side (WebP/raw-gray → JPEG de inspección).
const jpegQuality = 85

// decodeAndWrite valida el frame según su codec y lo persiste. Devuelve el
// nombre de archivo dentro del dir de la sesión y los bytes escritos.
func (s *SessionStore) decodeAndWrite(f reassembler.CompleteFrame) (string, int, error) {
	switch f.Codec {
	case protocol.CodecJPEG:
		// Validar que decodifica sin error (PRD §4) y persistir tal cual.
		if _, err := jpeg.Decode(bytes.NewReader(f.Data)); err != nil {
			return "", 0, fmt.Errorf("jpeg inválido: %w", err)
		}
		return s.writeBytes(f.FrameID, "jpg", f.Data)

	case protocol.CodecWebP:
		// Validar WebP y re-encodear a JPEG para que la inspección visual
		// (GET /frames/{id}.jpg) sea homogénea.
		img, err := webp.Decode(bytes.NewReader(f.Data))
		if err != nil {
			return "", 0, fmt.Errorf("webp inválido: %w", err)
		}
		return s.writeJPEG(f.FrameID, img)

	case protocol.CodecRawGray:
		// El payload ES la imagen: plano Y crudo (E9). Validar tamaño exacto.
		w, h := s.preset.Width, s.preset.Height
		if len(f.Data) != w*h {
			return "", 0, fmt.Errorf("raw-gray: %d bytes, esperaba %d (%dx%d)", len(f.Data), w*h, w, h)
		}
		img := &image.Gray{Pix: f.Data, Stride: w, Rect: image.Rect(0, 0, w, h)}
		return s.writeJPEG(f.FrameID, img)

	case protocol.CodecH264:
		// Persistir el Annex-B crudo siempre; decodificar con ffmpeg si está.
		file, n, err := s.writeBytes(f.FrameID, "h264", f.Data)
		if err != nil {
			return "", 0, err
		}
		if s.ffmpegPath == "" {
			s.mu.Lock()
			s.skippedDecode = true
			s.mu.Unlock()
			return file, n, nil
		}
		jpgFile, jn, err := s.ffmpegToJPEG(f.FrameID, file)
		if err != nil {
			// El .h264 quedó persistido; el decode fallido cuenta como decodeError.
			return "", 0, fmt.Errorf("ffmpeg decode: %w", err)
		}
		return jpgFile, jn, nil

	default:
		return "", 0, fmt.Errorf("codec desconocido %d", f.Codec)
	}
}

func (s *SessionStore) writeBytes(frameID uint16, ext string, data []byte) (string, int, error) {
	file := fmt.Sprintf("%d.%s", frameID, ext)
	if err := os.WriteFile(filepath.Join(s.dir, file), data, 0o644); err != nil {
		return "", 0, fmt.Errorf("write %s: %w", file, err)
	}
	return file, len(data), nil
}

func (s *SessionStore) writeJPEG(frameID uint16, img image.Image) (string, int, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return "", 0, fmt.Errorf("jpeg encode: %w", err)
	}
	return s.writeBytes(frameID, "jpg", buf.Bytes())
}
