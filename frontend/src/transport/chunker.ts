// Parte un frame codificado en chunks binarios con header de 12 B (PRD §4).
// Agnóstico del canal: el transporte (WebRTC DataChannel o WebSocket) recibe
// los Uint8Array tal cual.

import { encodeChunk, MAX_PAYLOAD } from '../protocol/header';

export interface ChunkFrameOptions {
  sessionSeq: number;
  frameId: number;
  codec: number;
  flags: number;
  data: Uint8Array;
  /** Tamaño máximo de payload por chunk. Default y tope: 1100 B. */
  maxPayload?: number;
}

const MAX_CHUNKS = 255; // totalChunks es uint8 y debe ser >= 1

export function chunkFrame({
  sessionSeq,
  frameId,
  codec,
  flags,
  data,
  maxPayload = MAX_PAYLOAD,
}: ChunkFrameOptions): Uint8Array[] {
  if (!Number.isInteger(maxPayload) || maxPayload < 1 || maxPayload > MAX_PAYLOAD) {
    throw new Error(`INVALID_MAX_PAYLOAD: ${maxPayload} (rango valido: 1..${MAX_PAYLOAD})`);
  }
  if (data.length === 0) {
    throw new Error('EMPTY_FRAME: data no puede estar vacio');
  }
  const totalChunks = Math.ceil(data.length / maxPayload);
  if (totalChunks > MAX_CHUNKS) {
    throw new Error(
      `TOO_MANY_CHUNKS: ${data.length} B requieren ${totalChunks} chunks (max ${MAX_CHUNKS})`,
    );
  }

  const chunks: Uint8Array[] = new Array(totalChunks);
  for (let i = 0; i < totalChunks; i++) {
    const payload = data.subarray(i * maxPayload, Math.min((i + 1) * maxPayload, data.length));
    chunks[i] = encodeChunk(
      { sessionSeq, frameId, chunkIndex: i, totalChunks, flags, codec },
      payload,
    );
  }
  return chunks;
}
