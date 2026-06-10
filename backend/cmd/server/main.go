package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"face-capture-poc/backend/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg := config.FromEnv()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", cfg.HTTPPort)
	log.Info("server starting", "addr", addr, "dataDir", cfg.DataDir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
