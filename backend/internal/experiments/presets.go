// Package experiments define los presets E1–E9 del PRD §6. Los presets viven
// en el backend (GET /experiments) para poder ajustarlos sin redeploy del
// frontend.
package experiments

import "face-capture-poc/backend/internal/protocol"

// Preset describe una configuración de experimento seleccionable.
type Preset struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Grayscale bool           `json:"grayscale"`
	Codec     protocol.Codec `json:"codec"`
	// Quality es la calidad de encode 0..1; 0 cuando el codec es
	// bitrate-driven (E8) o no hay encode (E9).
	Quality     float64 `json:"quality"`
	FPS         int     `json:"fps"`
	BitrateKbps int     `json:"bitrateKbps"`
	// ForceWebSocket fuerza el canal WS aunque WebRTC esté disponible (E7).
	ForceWebSocket bool `json:"forceWebSocket"`
	// WifiOnly marca presets cuyo bitrate no tiene sentido en 3G (E9).
	WifiOnly bool `json:"wifiOnly"`
	// RequiresFeature: el frontend oculta el preset si la feature no está
	// soportada. Valores: "webcodecs-h264" (E8), "mstp" (E9).
	RequiresFeature string `json:"requiresFeature,omitempty"`
}

var presets = []Preset{
	{ID: "E1-baseline-3g", Label: "160×120 gris JPEG q0.5 @10fps (3G baseline)", Width: 160, Height: 120, Grayscale: true, Codec: protocol.CodecJPEG, Quality: 0.5, FPS: 10, BitrateKbps: 250},
	{ID: "E2-gray-mid", Label: "240×180 gris JPEG q0.5 @10fps", Width: 240, Height: 180, Grayscale: true, Codec: protocol.CodecJPEG, Quality: 0.5, FPS: 10, BitrateKbps: 400},
	{ID: "E3-color-low", Label: "240×180 color JPEG q0.4 @8fps", Width: 240, Height: 180, Grayscale: false, Codec: protocol.CodecJPEG, Quality: 0.4, FPS: 8, BitrateKbps: 400},
	{ID: "E4-color-mid", Label: "320×240 color JPEG q0.5 @5fps", Width: 320, Height: 240, Grayscale: false, Codec: protocol.CodecJPEG, Quality: 0.5, FPS: 5, BitrateKbps: 400},
	{ID: "E5-webp", Label: "240×180 color WebP q0.5 @10fps", Width: 240, Height: 180, Grayscale: false, Codec: protocol.CodecWebP, Quality: 0.5, FPS: 10, BitrateKbps: 400},
	{ID: "E6-wifi-max", Label: "320×240 color JPEG q0.7 @15fps (WiFi)", Width: 320, Height: 240, Grayscale: false, Codec: protocol.CodecJPEG, Quality: 0.7, FPS: 15, BitrateKbps: 1500},
	{ID: "E7-ws-baseline", Label: "Igual a E2 forzando WebSocket (control TCP)", Width: 240, Height: 180, Grayscale: true, Codec: protocol.CodecJPEG, Quality: 0.5, FPS: 10, BitrateKbps: 400, ForceWebSocket: true},
	{ID: "E8-webcodecs-hw", Label: "320×240 color H.264 hardware (WebCodecs) @10fps", Width: 320, Height: 240, Grayscale: false, Codec: protocol.CodecH264, Quality: 0, FPS: 10, BitrateKbps: 400, RequiresFeature: "webcodecs-h264"},
	// E9 sin RequiresFeature: la extracción de luma se hace con un loop entero
	// sobre getImageData (universal); MSTP solo se reporta en client-stats.
	{ID: "E9-raw-gray", Label: "160×120 plano Y crudo sin encode @10fps (WiFi only)", Width: 160, Height: 120, Grayscale: true, Codec: protocol.CodecRawGray, Quality: 0, FPS: 10, BitrateKbps: 1600, WifiOnly: true},
}

// All devuelve una copia de los 9 presets en orden E1..E9.
func All() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// ByID busca un preset por su id exacto.
func ByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
