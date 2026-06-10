import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PacedSender } from './sender';
import type { ChunkTransport, ControlMessage } from './types';

// Transporte fake: registra timestamps de envío y permite simular backpressure.
class FakeTransport implements ChunkTransport {
  readonly kind = 'websocket' as const;
  bufferedAmount = 0;
  sent: { chunk: Uint8Array; at: number }[] = [];
  controls: ControlMessage[] = [];

  send(chunk: Uint8Array): void {
    this.sent.push({ chunk, at: Date.now() });
  }
  sendControl(msg: ControlMessage): void {
    this.controls.push(msg);
  }
  close(): void {}
}

function chunksOf(n: number, size = 1112, tag = 0): Uint8Array[] {
  return Array.from({ length: n }, (_, i) => {
    const c = new Uint8Array(size);
    c[0] = tag;
    c[1] = i;
    return c;
  });
}

describe('PacedSender', () => {
  let transport: FakeTransport;

  beforeEach(() => {
    vi.useFakeTimers();
    transport = new FakeTransport();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('a 400 kbps espacia chunks ~22ms, sin ráfagas', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000 });
    expect(sender.intervalMs).toBeCloseTo(22.24, 1);

    sender.enqueueFrame(chunksOf(5));
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 6);

    expect(transport.sent).toHaveLength(5);
    for (let i = 1; i < transport.sent.length; i++) {
      const gap = transport.sent[i].at - transport.sent[i - 1].at;
      expect(gap).toBeGreaterThanOrEqual(Math.floor(sender.intervalMs));
    }
  });

  it('saltea ticks bajo backpressure y reanuda al drenar', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000 });
    sender.enqueueFrame(chunksOf(3));

    await vi.advanceTimersByTimeAsync(1); // primer chunk sale
    expect(transport.sent).toHaveLength(1);

    transport.bufferedAmount = 100 * 1024; // canal saturado
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 5);
    expect(transport.sent).toHaveLength(1); // nada salió
    expect(sender.stats.ticksSkipped).toBeGreaterThan(0);

    transport.bufferedAmount = 0;
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 3);
    expect(transport.sent).toHaveLength(3);
  });

  it('evicta el frame más viejo entero al llenarse la cola', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000, maxQueuedFrames: 3 });

    // 4 frames sin dejar correr el reloj: la cola admite 3.
    sender.enqueueFrame(chunksOf(2, 1112, 1));
    sender.enqueueFrame(chunksOf(2, 1112, 2));
    sender.enqueueFrame(chunksOf(2, 1112, 3));
    sender.enqueueFrame(chunksOf(2, 1112, 4));

    expect(sender.stats.framesDropped).toBe(1);
    expect(sender.stats.framesEnqueued).toBe(4);

    const done = sender.flushAndStop();
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 10);
    await done;

    // El frame 1 (más viejo, no comenzado) fue evictado entero.
    const tags = transport.sent.map((s) => s.chunk[0]);
    expect(tags).not.toContain(1);
    expect(new Set(tags)).toEqual(new Set([2, 3, 4]));
  });

  it('no evicta el frame head si ya comenzó a enviarse', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000, maxQueuedFrames: 2 });

    sender.enqueueFrame(chunksOf(3, 1112, 1));
    await vi.advanceTimersByTimeAsync(1); // head (tag 1) ya envió un chunk
    sender.enqueueFrame(chunksOf(1, 1112, 2));
    sender.enqueueFrame(chunksOf(1, 1112, 3)); // cola llena → evicta tag 2, no el head

    const done = sender.flushAndStop();
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 10);
    await done;

    const tags = transport.sent.map((s) => s.chunk[0]);
    expect(tags.filter((t) => t === 1)).toHaveLength(3); // head completo
    expect(tags).not.toContain(2);
    expect(tags).toContain(3);
  });

  it('flushAndStop drena exactamente lo encolado y rechaza frames nuevos', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000 });
    sender.enqueueFrame(chunksOf(4));

    const done = sender.flushAndStop();
    expect(sender.enqueueFrame(chunksOf(2))).toBe(false);

    await vi.advanceTimersByTimeAsync(sender.intervalMs * 6);
    await done;

    expect(transport.sent).toHaveLength(4);
    expect(sender.stats.chunksSent).toBe(4);
  });

  it('flushAndStop con cola vacía resuelve inmediato', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000 });
    await expect(sender.flushAndStop()).resolves.toBeUndefined();
  });

  it('abort descarta lo no enviado', async () => {
    const sender = new PacedSender(transport, { bitrateBps: 400_000 });
    sender.enqueueFrame(chunksOf(10));
    await vi.advanceTimersByTimeAsync(1);
    sender.abort();
    await vi.advanceTimersByTimeAsync(sender.intervalMs * 20);
    expect(transport.sent.length).toBeLessThan(10);
  });
});
