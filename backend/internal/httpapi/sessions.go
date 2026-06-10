package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"face-capture-poc/backend/internal/experiments"
)

const maxBodyBytes = 64 << 10 // client-stats y create son JSON chicos

func (s *Server) handleExperiments(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, experiments.All())
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PresetID string `json:"presetId"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "json inválido")
		return
	}
	preset, ok := experiments.ByID(req.PresetID)
	if !ok {
		writeError(w, http.StatusBadRequest, "presetId desconocido: "+req.PresetID)
		return
	}
	sess, err := s.mgr.Create(preset)
	if err != nil {
		s.log.Error("create session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "no se pudo crear la sesión")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"sessionId":  sess.ID,
		"sessionSeq": sess.Seq,
		"preset":     sess.Preset,
		"stunUrl":    s.cfg.STUNURL,
	})
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.ByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "sesión desconocida")
		return
	}
	resp := map[string]any{
		"sessionId": sess.ID,
		"presetId":  sess.Preset.ID,
		"state":     sess.State(),
		"transport": sess.Transport(),
		"live":      sess.Reasm.Live(),
	}
	if rep := sess.Report(); rep != nil {
		resp["report"] = rep
		resp["decodeSkipped"] = sess.Store.DecodeSkipped()
		if file, n := sess.Store.CompositeFile(); file != "" {
			resp["composite"] = "/sessions/" + sess.ID + "/frames/" + file
			resp["compositeFrames"] = n
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListFrames(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.ByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "sesión desconocida")
		return
	}
	type frameEntry struct {
		FrameID     uint16  `json:"frameId"`
		State       string  `json:"state"`
		ReceivedPct float64 `json:"receivedPct"`
		URL         string  `json:"url,omitempty"`
	}
	entries := []frameEntry{}
	for _, f := range sess.Store.List() {
		entries = append(entries, frameEntry{
			FrameID:     f.FrameID,
			State:       f.State,
			ReceivedPct: 100,
			URL:         "/sessions/" + sess.ID + "/frames/" + f.File,
		})
	}
	if rep := sess.Report(); rep != nil {
		for _, p := range rep.Partials {
			entries = append(entries, frameEntry{FrameID: p.FrameID, State: "partial", ReceivedPct: p.ReceivedPct})
		}
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) handleServeFrame(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.ByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "sesión desconocida")
		return
	}
	// FramePath confina con filepath.Base: sin path traversal.
	http.ServeFile(w, r, sess.Store.FramePath(r.PathValue("file")))
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.ByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "sesión desconocida")
		return
	}
	b, err := sess.Store.ReadReport()
	if err != nil {
		writeError(w, http.StatusNotFound, "report aún no consolidado")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *Server) handleClientStats(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.mgr.ByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "sesión desconocida")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		writeError(w, http.StatusBadRequest, "client-stats debe ser JSON")
		return
	}
	sess.SetClientStats(raw)
	s.log.Info("client stats attached", "sessionId", sess.ID, "bytes", len(raw))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
