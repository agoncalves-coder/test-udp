package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/httpapi"
	"face-capture-poc/backend/internal/protocol"
	"face-capture-poc/backend/internal/reassembler"
	"face-capture-poc/backend/internal/session"
	"face-capture-poc/backend/internal/transport"
)

// Gate automatizado de la Fase 1 (PRD §9): captura sintética de 3 frames JPEG
// por WebSocket real, con un chunk deliberadamente omitido, debe terminar en
// 2 frames completos + 1 parcial, con el frame completo servible y decodificable.
func TestWSEndToEnd(t *testing.T) {
	cfg := config.Config{
		HTTPPort:       0,
		STUNURL:        "stun:stun.example:19302",
		DataDir:        t.TempDir(),
		AllowedOrigins: []string{"http://localhost:5173"},
	}
	// Timeouts cortos para que el test consolide rápido con clock real.
	mcfg := session.DefaultManagerConfig(cfg.DataDir, "")
	mcfg.Reasm = reassembler.Config{
		FrameTimeout: 300 * time.Millisecond,
		EndGrace:     400 * time.Millisecond,
		IdleTimeout:  2 * time.Second,
	}
	mcfg.SweepEvery = 50 * time.Millisecond
	mgr := session.NewManager(mcfg, nil)

	handler := httpapi.Handler(cfg, mgr, nil, transport.WSHandler(cfg, mgr, nil), nil)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 1. Crear sesión.
	resp, err := http.Post(srv.URL+"/sessions", "application/json", strings.NewReader(`{"presetId":"E2-gray-mid"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		SessionID  string `json:"sessionId"`
		SessionSeq uint16 `json:"sessionSeq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || created.SessionID == "" {
		t.Fatalf("POST /sessions: status %d, body %+v", resp.StatusCode, created)
	}

	// 2. Conectar WS.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/ws?session=" + created.SessionID
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// 3. Captura sintética: 3 frames JPEG reales, multichunk (maxPayload 100),
	//    omitiendo el chunk 1 del frame 1.
	jpegData := makeJPEG(t)
	totalSent := 0
	for frameID := uint16(0); frameID < 3; frameID++ {
		chunks := chunkBytes(t, created.SessionSeq, frameID, jpegData, 100)
		for i, c := range chunks {
			if frameID == 1 && i == 1 {
				continue // pérdida deliberada
			}
			if err := conn.Write(ctx, websocket.MessageBinary, c); err != nil {
				t.Fatal(err)
			}
			totalSent++
		}
	}

	// 4. END_OF_CAPTURE (3×, como el cliente real: el canal puede perderlo).
	end := fmt.Sprintf(`{"type":"end_of_capture","sessionId":%q,"framesSent":3,"chunksSent":%d}`, created.SessionID, totalSent)
	for i := 0; i < 3; i++ {
		if err := conn.Write(ctx, websocket.MessageText, []byte(end)); err != nil {
			t.Fatal(err)
		}
	}

	// 5. Poll hasta done (FrameTimeout 300ms + EndGrace 400ms ≪ 5s).
	var state struct {
		State  string `json:"state"`
		Report *struct {
			FramesComplete int `json:"framesComplete"`
			FramesPartial  int `json:"framesPartial"`
			FramesLost     int `json:"framesLost"`
			DecodeErrors   int `json:"decodeErrors"`
		} `json:"report"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("la sesión no consolidó a tiempo: estado %+v", state)
		}
		r, err := http.Get(srv.URL + "/sessions/" + created.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(r.Body).Decode(&state)
		r.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if state.State == "done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 6. Métricas del report.
	if state.Report == nil {
		t.Fatal("report ausente con state done")
	}
	if state.Report.FramesComplete != 2 || state.Report.FramesPartial != 1 || state.Report.FramesLost != 0 {
		t.Errorf("complete/partial/lost = %d/%d/%d, esperaba 2/1/0",
			state.Report.FramesComplete, state.Report.FramesPartial, state.Report.FramesLost)
	}
	if state.Report.DecodeErrors != 0 {
		t.Errorf("decodeErrors = %d, esperaba 0", state.Report.DecodeErrors)
	}

	// 7. El frame completo se sirve y decodifica como JPEG.
	fr, err := http.Get(srv.URL + "/sessions/" + created.SessionID + "/frames/0.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer fr.Body.Close()
	if fr.StatusCode != http.StatusOK {
		t.Fatalf("GET frame: status %d", fr.StatusCode)
	}
	body, _ := io.ReadAll(fr.Body)
	if _, err := jpeg.Decode(bytes.NewReader(body)); err != nil {
		t.Errorf("el frame servido no decodifica como JPEG: %v", err)
	}

	// 8. report.json persistido y accesible.
	rr, err := http.Get(srv.URL + "/sessions/" + created.SessionID + "/report.json")
	if err != nil {
		t.Fatal(err)
	}
	rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		t.Errorf("GET report.json: status %d", rr.StatusCode)
	}
}

// makeJPEG genera un JPEG real de 32x24 (≥3 chunks con maxPayload=100).
func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 32, 24))
	for i := range img.Pix {
		img.Pix[i] = uint8((i * 7) % 256)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 200 {
		t.Fatalf("jpeg de prueba demasiado chico (%d B) para multichunk", buf.Len())
	}
	return buf.Bytes()
}

// chunkBytes replica el chunker del frontend con protocol.EncodeChunk.
func chunkBytes(t *testing.T, seq, frameID uint16, data []byte, maxPayload int) [][]byte {
	t.Helper()
	total := (len(data) + maxPayload - 1) / maxPayload
	chunks := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		end := (i + 1) * maxPayload
		if end > len(data) {
			end = len(data)
		}
		payload := data[i*maxPayload : end]
		c, err := protocol.EncodeChunk(protocol.ChunkHeader{
			SessionSeq:  seq,
			FrameID:     frameID,
			ChunkIndex:  uint8(i),
			TotalChunks: uint8(total),
			PayloadLen:  uint16(len(payload)),
			Codec:       protocol.CodecJPEG,
		}, payload)
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, c)
	}
	return chunks
}
