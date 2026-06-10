package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/httpapi"
	"face-capture-poc/backend/internal/session"
	"face-capture-poc/backend/internal/storage"
	"face-capture-poc/backend/internal/transport"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg := config.FromEnv()

	ffmpegPath := storage.FFmpegPath()
	if ffmpegPath == "" {
		log.Warn("ffmpeg no encontrado: los frames H.264 (E8) se persistirán sin decodificar (decodeSkipped)")
	}

	mgr := session.NewManager(session.DefaultManagerConfig(cfg.DataDir, ffmpegPath), log)

	wsHandler := transport.WSHandler(cfg, mgr, log)
	handler := httpapi.Handler(cfg, mgr, log, wsHandler, nil)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("server starting", "addr", srv.Addr, "dataDir", cfg.DataDir, "allowedOrigins", cfg.AllowedOrigins)
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
