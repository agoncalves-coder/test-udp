// Abstracción de canal: el chunker, el sender y el capture loop no saben si
// están sobre WebRTC DataChannel o WebSocket — el protocolo de aplicación es
// idéntico en ambos (PRD §2). Binario = chunks; texto JSON = control.

export interface ChunkTransport {
  readonly kind: 'webrtc' | 'websocket';
  /** Bytes encolados sin enviar; RTCDataChannel y WebSocket lo exponen igual. */
  readonly bufferedAmount: number;
  send(chunk: Uint8Array): void;
  sendControl(msg: ControlMessage): void;
  close(): void;
  /** Resumen de RTCPeerConnection.getStats() si el canal lo soporta (PRD §7). */
  stats?(): Promise<Record<string, unknown>>;
}

export interface EndOfCapture {
  type: 'end_of_capture';
  sessionId: string;
  framesSent: number;
  chunksSent: number;
  captureStartMs: number;
  captureEndMs: number;
}

export type ControlMessage = EndOfCapture;
