// PacedSender: pacing obligatorio del PRD §4 — nada de ráfagas. Un chunk por
// tick a intervalo derivado del bitrate objetivo (~22ms @ 400kbps), con
// bufferedAmount como backpressure y evicción de frames enteros cuando la
// captura va más rápido que la red (nunca bloquear la captura).
import { MAX_PAYLOAD, HEADER_SIZE } from '../protocol/header';
import type { ChunkTransport } from './types';

export interface SenderConfig {
  bitrateBps: number;
  /** Umbral de backpressure sobre transport.bufferedAmount (PRD: 64 KB). */
  maxBufferedBytes?: number;
  /** Frames en cola antes de evictar el más viejo (capturar > enviar). */
  maxQueuedFrames?: number;
}

export interface SenderStats {
  framesEnqueued: number;
  framesDropped: number;
  chunksSent: number;
  bytesSent: number;
  ticksSkipped: number;
}

interface QueuedFrame {
  chunks: Uint8Array[];
  next: number; // índice del próximo chunk a enviar
}

const MAX_CHUNK_BYTES = HEADER_SIZE + MAX_PAYLOAD; // 1112

export class PacedSender {
  readonly transport: ChunkTransport;
  readonly intervalMs: number;
  readonly stats: SenderStats = {
    framesEnqueued: 0,
    framesDropped: 0,
    chunksSent: 0,
    bytesSent: 0,
    ticksSkipped: 0,
  };

  private readonly maxBufferedBytes: number;
  private readonly maxQueuedFrames: number;
  private queue: QueuedFrame[] = [];
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private drainResolve: (() => void) | null = null;

  constructor(transport: ChunkTransport, cfg: SenderConfig) {
    this.transport = transport;
    this.maxBufferedBytes = cfg.maxBufferedBytes ?? 64 * 1024;
    this.maxQueuedFrames = cfg.maxQueuedFrames ?? 3;
    // Un chunk (≤1112 B) por tick para sostener el bitrate objetivo.
    this.intervalMs = (MAX_CHUNK_BYTES * 8 * 1000) / cfg.bitrateBps;
  }

  /**
   * Encola un frame entero (sus chunks). Si la cola está llena, evicta el
   * frame más viejo aún no comenzado — un frame a medio enviar ya generó
   * tráfico; abortarlo garantiza un parcial sin ahorrar casi nada.
   * Devuelve false si el sender ya está parado (frame descartado).
   */
  enqueueFrame(chunks: Uint8Array[]): boolean {
    if (this.stopped || chunks.length === 0) {
      return false;
    }
    while (this.queue.length >= this.maxQueuedFrames) {
      const headStarted = this.queue[0].next > 0;
      const evictIdx = headStarted && this.queue.length > 1 ? 1 : 0;
      this.queue.splice(evictIdx, 1);
      this.stats.framesDropped++;
    }
    this.queue.push({ chunks, next: 0 });
    this.stats.framesEnqueued++;
    this.schedule(0);
    return true;
  }

  /** Drena lo encolado al ritmo del pacing y para. */
  flushAndStop(): Promise<void> {
    this.stopped = true;
    if (this.queue.length === 0) {
      this.clearTimer();
      return Promise.resolve();
    }
    return new Promise((resolve) => {
      this.drainResolve = resolve;
      this.schedule(0);
    });
  }

  /** Corta inmediatamente, descartando lo no enviado. */
  abort(): void {
    this.stopped = true;
    this.queue = [];
    this.clearTimer();
    this.drainResolve?.();
    this.drainResolve = null;
  }

  private schedule(delayMs: number): void {
    if (this.timer !== null) {
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = null;
      this.tick();
    }, delayMs);
  }

  private tick(): void {
    if (this.queue.length === 0) {
      this.drainResolve?.();
      this.drainResolve = null;
      return;
    }
    // Backpressure: si el canal acumula >64 KB sin enviar, saltear el tick y
    // dejar drenar (PRD §4). La cola sigue acotada por la evicción.
    if (this.transport.bufferedAmount > this.maxBufferedBytes) {
      this.stats.ticksSkipped++;
      this.schedule(this.intervalMs);
      return;
    }

    const head = this.queue[0];
    const chunk = head.chunks[head.next++];
    this.transport.send(chunk);
    this.stats.chunksSent++;
    this.stats.bytesSent += chunk.byteLength;
    if (head.next >= head.chunks.length) {
      this.queue.shift();
    }

    if (this.queue.length > 0) {
      this.schedule(this.intervalMs);
    } else if (this.drainResolve) {
      this.drainResolve();
      this.drainResolve = null;
    }
  }

  private clearTimer(): void {
    if (this.timer !== null) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
