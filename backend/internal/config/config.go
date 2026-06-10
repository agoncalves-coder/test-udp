// Package config carga la configuración del servidor desde variables de entorno.
package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort       int
	UDPPort        int
	PublicIP       string   // IP pública para SetNAT1To1IPs; vacía en dev local
	STUNURL        string   // se entrega a los clientes en POST /sessions
	DataDir        string   // raíz de persistencia de frames y reports
	AllowedOrigins []string // orígenes exactos para CORS y validación de WS
	FlyAppName     string   // no vacío cuando corre en Fly.io (bind a fly-global-services)
}

func FromEnv() Config {
	return Config{
		HTTPPort:       intEnv("HTTP_PORT", 8080),
		UDPPort:        intEnv("UDP_PORT", 5004),
		PublicIP:       os.Getenv("PUBLIC_IP"),
		STUNURL:        strEnv("STUN_URL", "stun:stun.l.google.com:19302"),
		DataDir:        strEnv("DATA_DIR", "./data"),
		AllowedOrigins: splitEnv("ALLOWED_ORIGINS", "http://localhost:5173"),
		FlyAppName:     os.Getenv("FLY_APP_NAME"),
	}
}

func strEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitEnv(key, def string) []string {
	raw := strEnv(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
