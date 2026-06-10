// Espejo del Preset que sirve el backend en GET /experiments
// (backend/internal/experiments/presets.go). Los presets viven en el backend
// para poder ajustarlos sin redeploy del frontend (PRD §6).

export interface Preset {
  id: string;
  label: string;
  width: number;
  height: number;
  grayscale: boolean;
  codec: number; // protocol.Codec: 0=JPEG 1=WebP 2=raw-gray 3=h264
  quality: number;
  fps: number;
  bitrateKbps: number;
  forceWebSocket: boolean;
  wifiOnly: boolean;
  requiresFeature?: 'webcodecs-h264' | 'mstp';
}
