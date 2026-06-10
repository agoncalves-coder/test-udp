package storage

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"face-capture-poc/backend/internal/experiments"
	"face-capture-poc/backend/internal/protocol"
	"face-capture-poc/backend/internal/reassembler"
)

func testStore(t *testing.T, preset experiments.Preset, ffmpegPath string) *SessionStore {
	t.Helper()
	s, err := NewSessionStore(t.TempDir(), "test-session", preset, ffmpegPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func frame(codec protocol.Codec, data []byte) reassembler.CompleteFrame {
	return reassembler.CompleteFrame{FrameID: 7, Codec: codec, Data: data, FirstChunkAt: time.Now(), CompletedAt: time.Now()}
}

func tinyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 256)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestJPEGValidAndPersisted(t *testing.T) {
	preset, _ := experiments.ByID("E2-gray-mid")
	s := testStore(t, preset, "")

	data := tinyJPEG(t, 8, 8)
	if err := s.OnFrameComplete(frame(protocol.CodecJPEG, data)); err != nil {
		t.Fatalf("jpeg válido rechazado: %v", err)
	}

	list := s.List()
	if len(list) != 1 || list[0].FrameID != 7 || list[0].File != "7.jpg" {
		t.Fatalf("List() = %+v", list)
	}
	onDisk, err := os.ReadFile(s.FramePath(list[0].File))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, data) {
		t.Error("el JPEG debe persistirse byte a byte")
	}
}

func TestCorruptJPEGCountsAsDecodeError(t *testing.T) {
	preset, _ := experiments.ByID("E2-gray-mid")
	s := testStore(t, preset, "")

	if err := s.OnFrameComplete(frame(protocol.CodecJPEG, []byte("no soy un jpeg"))); err == nil {
		t.Fatal("jpeg corrupto debe devolver error (decodeError en el report)")
	}
	if len(s.List()) != 0 {
		t.Error("un frame corrupto no debe persistirse")
	}
}

func TestRawGrayToJPEG(t *testing.T) {
	preset, _ := experiments.ByID("E9-raw-gray") // 160x120
	s := testStore(t, preset, "")

	data := make([]byte, 160*120)
	for i := range data {
		data[i] = uint8(i % 256)
	}
	if err := s.OnFrameComplete(frame(protocol.CodecRawGray, data)); err != nil {
		t.Fatalf("raw-gray válido rechazado: %v", err)
	}

	onDisk, err := os.ReadFile(s.FramePath("7.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := image.Decode(bytes.NewReader(onDisk))
	if err != nil || format != "jpeg" {
		t.Fatalf("la salida debe ser JPEG decodificable: format=%q err=%v", format, err)
	}
	if b := img.Bounds(); b.Dx() != 160 || b.Dy() != 120 {
		t.Errorf("bounds = %v, esperaba 160x120", b)
	}
}

func TestRawGrayWrongSize(t *testing.T) {
	preset, _ := experiments.ByID("E9-raw-gray")
	s := testStore(t, preset, "")

	if err := s.OnFrameComplete(frame(protocol.CodecRawGray, make([]byte, 100))); err == nil {
		t.Fatal("raw-gray con tamaño incorrecto debe rechazarse")
	}
}

func TestH264WithoutFfmpegSkipsDecode(t *testing.T) {
	preset, _ := experiments.ByID("E8-webcodecs-hw")
	s := testStore(t, preset, "") // sin ffmpeg

	if err := s.OnFrameComplete(frame(protocol.CodecH264, []byte{0, 0, 0, 1, 0x67})); err != nil {
		t.Fatalf("h264 sin ffmpeg no es un error: %v", err)
	}
	if !s.DecodeSkipped() {
		t.Error("DecodeSkipped debe ser true sin ffmpeg")
	}
	if _, err := os.Stat(s.FramePath("7.h264")); err != nil {
		t.Errorf("el .h264 crudo debe persistirse igual: %v", err)
	}
}

func TestWriteAndReadReport(t *testing.T) {
	preset, _ := experiments.ByID("E2-gray-mid")
	s := testStore(t, preset, "")

	rep := FinalReport{SessionID: "test-session", PresetID: preset.ID, Transport: "websocket",
		Report: reassembler.Report{FramesComplete: 3}}
	if err := s.WriteReport(rep); err != nil {
		t.Fatal(err)
	}
	b, err := s.ReadReport()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"framesComplete": 3`)) {
		t.Errorf("report.json no contiene las métricas: %s", b)
	}
}

func TestFramePathTraversalSafe(t *testing.T) {
	preset, _ := experiments.ByID("E2-gray-mid")
	s := testStore(t, preset, "")

	p := s.FramePath("../../etc/passwd")
	if filepath.Dir(p) != s.dir {
		t.Errorf("FramePath debe confinar al dir de la sesión: %s", p)
	}
}
