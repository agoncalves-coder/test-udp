// Selección de canal (PRD §3): WebRTC DataChannel unreliable como principal,
// WebSocket como fallback automático si la negociación no cierra en <4s.
// Fase 1: solo WS. La Fase 2 enchufa webrtc.ts en el slot marcado.
import { wsUrl } from '../api/client';
import type { ChunkTransport } from './types';
import { connectWebSocket } from './websocket';

export interface ConnectOptions {
  sessionId: string;
  stunUrl: string;
  forceWebSocket: boolean;
  /** Presupuesto para WebRTC antes de caer a WS (PRD: 4000ms). */
  timeoutMs?: number;
}

export interface ConnectResult {
  transport: ChunkTransport;
  /** Por qué NO se usó WebRTC (telemetría de campo, va en client-stats). */
  fallbackReason?: string;
}

const WS_CONNECT_TIMEOUT_MS = 5000;

export async function connectTransport(opts: ConnectOptions): Promise<ConnectResult> {
  if (opts.forceWebSocket) {
    const transport = await connectWebSocket(wsUrl(opts.sessionId), WS_CONNECT_TIMEOUT_MS);
    return { transport, fallbackReason: 'forced-by-preset' };
  }

  // TODO(F2): race de connectWebRTC(opts) contra timeoutMs → fallback WS.
  const transport = await connectWebSocket(wsUrl(opts.sessionId), WS_CONNECT_TIMEOUT_MS);
  return { transport, fallbackReason: 'webrtc-not-implemented' };
}
