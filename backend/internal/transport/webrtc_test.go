package transport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/httpapi"
	"face-capture-poc/backend/internal/reassembler"
	"face-capture-poc/backend/internal/session"
	"face-capture-poc/backend/internal/transport"
)

// Gate de la Fase 2 sin browser: un cliente pion con DataChannel
// {ordered:false, maxRetransmits:0} negocia contra el engine (UDPMux real,
// señalización non-trickle por POST /signal) y manda una captura sintética
// con un chunk perdido. Mismo assert que el e2e de WebSocket.
func TestWebRTCEndToEnd(t *testing.T) {
	cfg := config.Config{
		UDPPort:        freeUDPPort(t),
		STUNURL:        "stun:stun.example:19302",
		DataDir:        t.TempDir(),
		AllowedOrigins: []string{"http://localhost:5173"},
	}
	mcfg := session.DefaultManagerConfig(cfg.DataDir, "")
	mcfg.Reasm = reassembler.Config{
		FrameTimeout: 300 * time.Millisecond,
		EndGrace:     400 * time.Millisecond,
		IdleTimeout:  2 * time.Second,
	}
	mcfg.SweepEvery = 50 * time.Millisecond
	mgr := session.NewManager(mcfg, nil)

	engine, err := transport.NewWebRTCEngine(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.Handler(cfg, mgr, nil, nil, transport.SignalHandler(engine, mgr, nil))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Sesión.
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

	// Cliente WebRTC (como el browser): canal unreliable + offer non-trickle.
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	ordered := false
	var maxRetransmits uint16 = 0
	dc, err := pc.CreateDataChannel("data", &webrtc.DataChannelInit{
		Ordered:        &ordered,
		MaxRetransmits: &maxRetransmits,
	})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered

	// POST /signal.
	body, _ := json.Marshal(map[string]any{"sessionId": created.SessionID, "offer": pc.LocalDescription()})
	sigResp, err := http.Post(srv.URL+"/signal", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if sigResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /signal: status %d", sigResp.StatusCode)
	}
	var sig struct {
		Answer webrtc.SessionDescription `json:"answer"`
	}
	if err := json.NewDecoder(sigResp.Body).Decode(&sig); err != nil {
		t.Fatal(err)
	}
	sigResp.Body.Close()
	if err := pc.SetRemoteDescription(sig.Answer); err != nil {
		t.Fatal(err)
	}

	select {
	case <-opened:
	case <-time.After(10 * time.Second):
		t.Fatal("el datachannel no abrió en 10s")
	}

	// Captura sintética: 3 frames, chunk 1 del frame 1 omitido.
	jpegData := makeJPEG(t)
	totalSent := 0
	for frameID := uint16(0); frameID < 3; frameID++ {
		for i, c := range chunkBytes(t, created.SessionSeq, frameID, jpegData, 100) {
			if frameID == 1 && i == 1 {
				continue
			}
			if err := dc.Send(c); err != nil {
				t.Fatal(err)
			}
			totalSent++
		}
	}
	end := fmt.Sprintf(`{"type":"end_of_capture","sessionId":%q,"framesSent":3,"chunksSent":%d}`, created.SessionID, totalSent)
	for i := 0; i < 3; i++ {
		if err := dc.SendText(end); err != nil {
			t.Fatal(err)
		}
	}

	// Poll hasta done; el transporte reportado debe ser webrtc.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var state struct {
		State     string `json:"state"`
		Transport string `json:"transport"`
		Report    *struct {
			FramesComplete int `json:"framesComplete"`
			FramesPartial  int `json:"framesPartial"`
		} `json:"report"`
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("la sesión no consolidó: %+v", state)
		default:
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

	if state.Transport != "webrtc" {
		t.Errorf("transport = %q, esperaba webrtc", state.Transport)
	}
	if state.Report == nil || state.Report.FramesComplete != 2 || state.Report.FramesPartial != 1 {
		t.Errorf("report = %+v, esperaba 2 completos / 1 parcial", state.Report)
	}
}

// freeUDPPort encuentra un puerto UDP libre para el mux del test.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}
