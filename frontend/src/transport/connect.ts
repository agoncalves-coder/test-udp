// Selección de canal (PRD §3): WebRTC DataChannel unreliable como principal;
// si la negociación no cierra en <4s (NAT simétrico sin TURN, red corporativa,
// UDP bloqueado) → fallback automático a WebSocket. Mismo protocolo en ambos.
import { wsUrl } from '../api/client';
import type { ChunkTransport } from './types';
import { connectWebRTC } from './webrtc';
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

const WEBRTC_BUDGET_MS = 4000;
const WS_CONNECT_TIMEOUT_MS = 5000;

export async function connectTransport(opts: ConnectOptions): Promise<ConnectResult> {
  if (opts.forceWebSocket) {
    const transport = await connectWebSocket(wsUrl(opts.sessionId), WS_CONNECT_TIMEOUT_MS);
    return { transport, fallbackReason: 'forced-by-preset' };
  }

  const budget = opts.timeoutMs ?? WEBRTC_BUDGET_MS;
  const rtcAttempt = connectWebRTC({ sessionId: opts.sessionId, stunUrl: opts.stunUrl });
  try {
    const transport = await withTimeout(rtcAttempt, budget, `webrtc no conectó en ${budget}ms`);
    return { transport };
  } catch (e) {
    // Si la negociación llega a abrir después del timeout, cerrarla: ya
    // elegimos WS y una PeerConnection huérfana retiene la cámara de red.
    rtcAttempt.then((t) => t.close()).catch(() => {});
    const reason = e instanceof Error ? e.message : String(e);
    const transport = await connectWebSocket(wsUrl(opts.sessionId), WS_CONNECT_TIMEOUT_MS);
    return { transport, fallbackReason: reason };
  }
}

function withTimeout<T>(p: Promise<T>, ms: number, msg: string): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(msg)), ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (e) => {
        clearTimeout(timer);
        reject(e);
      },
    );
  });
}
