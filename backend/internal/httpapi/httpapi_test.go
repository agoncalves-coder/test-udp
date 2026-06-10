package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/session"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	cfg := config.Config{
		STUNURL:        "stun:stun.example:19302",
		DataDir:        t.TempDir(),
		AllowedOrigins: []string{"https://face-capture-poc.vercel.app", "http://localhost:5173"},
	}
	mgr := session.NewManager(session.DefaultManagerConfig(cfg.DataDir, ""), nil)
	return Handler(cfg, mgr, nil, nil, nil)
}

func TestExperimentsListsNinePresets(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/experiments", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var presets []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &presets); err != nil {
		t.Fatal(err)
	}
	if len(presets) != 9 {
		t.Errorf("presets = %d, esperaba 9 (E1-E9)", len(presets))
	}
}

func TestCreateSessionAssignsIncrementingSeq(t *testing.T) {
	h := testHandler(t)

	var prev uint16
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"presetId":"E1-baseline-3g"}`))
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
		}
		var out struct {
			SessionID  string `json:"sessionId"`
			SessionSeq uint16 `json:"sessionSeq"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.SessionID == "" || out.SessionSeq == 0 {
			t.Fatalf("respuesta incompleta: %+v", out)
		}
		if i > 0 && out.SessionSeq != prev+1 {
			t.Errorf("seq = %d, esperaba %d (monotónico)", out.SessionSeq, prev+1)
		}
		prev = out.SessionSeq
	}
}

func TestCreateSessionUnknownPreset(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"presetId":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, esperaba 400", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/sessions", nil)
	req.Header.Set("Origin", "https://face-capture-poc.vercel.app")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, esperaba 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://face-capture-poc.vercel.app" {
		t.Errorf("Allow-Origin = %q (debe ser echo del origin exacto, nunca *)", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Errorf("Allow-Methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
		t.Errorf("Allow-Headers = %q", got)
	}
}

func TestCORSUnknownOriginGetsNoHeader(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/experiments", nil)
	req.Header.Set("Origin", "https://evil.example")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin = %q para un origin no permitido, esperaba vacío", got)
	}
}

func TestGetSessionNotFound(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/desconocida", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, esperaba 404", rec.Code)
	}
}
