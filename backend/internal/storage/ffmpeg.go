package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// FFmpegPath resuelve el binario de ffmpeg al arrancar el server. Vacío si no
// está instalado: los frames E8 se persisten como .h264 y el report marca
// decodeSkipped (dev local en Windows sin ffmpeg, PRD §6).
func FFmpegPath() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

// ffmpegToJPEG decodifica un keyframe H.264 Annex-B (autocontenido: SPS/PPS
// in-band) a JPEG invocando ffmpeg como proceso (PRD §6: sin bindings cgo).
func (s *SessionStore) ffmpegToJPEG(frameID uint16, h264File string) (string, int, error) {
	in := filepath.Join(s.dir, h264File)
	out := fmt.Sprintf("%d.jpg", frameID)
	outPath := filepath.Join(s.dir, out)

	cmd := exec.Command(s.ffmpegPath,
		"-hide_banner", "-loglevel", "error",
		"-f", "h264", "-i", in,
		"-frames:v", "1", "-q:v", "2",
		"-y", outPath,
	)
	if outBytes, err := cmd.CombinedOutput(); err != nil {
		return "", 0, fmt.Errorf("%w: %s", err, outBytes)
	}
	fi, err := os.Stat(outPath)
	if err != nil {
		return "", 0, fmt.Errorf("ffmpeg no produjo salida: %w", err)
	}
	return out, int(fi.Size()), nil
}
