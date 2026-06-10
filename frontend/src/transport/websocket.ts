// Fallback WebSocket (TCP). Mismo protocolo de aplicación que el DataChannel.
import type { ChunkTransport, ControlMessage } from './types';

export function connectWebSocket(url: string, timeoutMs: number): Promise<ChunkTransport> {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';

    const timer = setTimeout(() => {
      ws.close();
      reject(new Error(`ws timeout tras ${timeoutMs}ms`));
    }, timeoutMs);

    ws.onopen = () => {
      clearTimeout(timer);
      resolve({
        kind: 'websocket',
        get bufferedAmount() {
          return ws.bufferedAmount;
        },
        send(chunk: Uint8Array) {
          // El chunker siempre crea buffers exactos no compartidos; el cast
          // salva el genérico Uint8Array<ArrayBufferLike> de TS 5.9.
          ws.send(chunk as Uint8Array<ArrayBuffer>);
        },
        sendControl(msg: ControlMessage) {
          ws.send(JSON.stringify(msg));
        },
        close() {
          ws.close(1000);
        },
      });
    };

    ws.onerror = () => {
      clearTimeout(timer);
      reject(new Error('ws error en la conexión'));
    };
  });
}
