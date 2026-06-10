import { describe, expect, it } from 'vitest';
import { Codec, decodeChunk, Flag, MAX_PAYLOAD } from '../protocol/header';
import { chunkFrame } from './chunker';

function makeData(length: number): Uint8Array {
  return Uint8Array.from({ length }, (_, i) => i & 0xff);
}

const base = { sessionSeq: 7, frameId: 42, codec: Codec.JPEG as number, flags: 0 };

describe('chunkFrame: límites de tamaño', () => {
  it('1100 B → 1 chunk', () => {
    const chunks = chunkFrame({ ...base, data: makeData(1100) });
    expect(chunks).toHaveLength(1);
    const { header } = decodeChunk(chunks[0]);
    expect(header.chunkIndex).toBe(0);
    expect(header.totalChunks).toBe(1);
    expect(header.payloadLen).toBe(1100);
  });

  it('1101 B → 2 chunks con payloadLens [1100, 1]', () => {
    const chunks = chunkFrame({ ...base, data: makeData(1101) });
    expect(chunks).toHaveLength(2);
    expect(chunks.map((c) => decodeChunk(c).header.payloadLen)).toEqual([1100, 1]);
  });

  it('19200 B (frame raw-gray 160×120, E9) → 18 chunks', () => {
    const chunks = chunkFrame({ ...base, codec: Codec.RawGray, data: makeData(19200) });
    expect(chunks).toHaveLength(18);
    const lens = chunks.map((c) => decodeChunk(c).header.payloadLen);
    expect(lens.slice(0, 17)).toEqual(new Array(17).fill(1100));
    expect(lens[17]).toBe(19200 - 17 * 1100);
  });

  it('255×1100 B (máximo absoluto) → 255 chunks', () => {
    const chunks = chunkFrame({ ...base, data: makeData(255 * MAX_PAYLOAD) });
    expect(chunks).toHaveLength(255);
  });
});

describe('chunkFrame: integridad del frame', () => {
  it('la concatenación de los payloads decodificados es idéntica al input', () => {
    const data = makeData(19200);
    const chunks = chunkFrame({ ...base, data });
    const reassembled = new Uint8Array(data.length);
    let offset = 0;
    for (const chunk of chunks) {
      const { payload } = decodeChunk(chunk);
      reassembled.set(payload, offset);
      offset += payload.length;
    }
    expect(offset).toBe(data.length);
    expect(reassembled).toEqual(data);
  });

  it('chunkIndex es secuencial y todos los headers comparten sessionSeq/frameId/totalChunks/codec', () => {
    const chunks = chunkFrame({ ...base, codec: Codec.WebP, data: makeData(3500) });
    const headers = chunks.map((c) => decodeChunk(c).header);
    headers.forEach((h, i) => {
      expect(h.chunkIndex).toBe(i);
      expect(h.totalChunks).toBe(chunks.length);
      expect(h.sessionSeq).toBe(base.sessionSeq);
      expect(h.frameId).toBe(base.frameId);
      expect(h.codec).toBe(Codec.WebP);
    });
  });

  it('flags se propagan a TODOS los chunks del frame', () => {
    const flags = Flag.Grayscale | Flag.LastFrame;
    const chunks = chunkFrame({ ...base, flags, data: makeData(5000) });
    expect(chunks.length).toBeGreaterThan(1);
    for (const chunk of chunks) {
      expect(decodeChunk(chunk).header.flags).toBe(flags);
    }
  });

  it('respeta maxPayload custom', () => {
    const chunks = chunkFrame({ ...base, data: makeData(250), maxPayload: 100 });
    expect(chunks.map((c) => decodeChunk(c).header.payloadLen)).toEqual([100, 100, 50]);
  });
});

describe('chunkFrame: errores', () => {
  it('lanza con data vacío', () => {
    expect(() => chunkFrame({ ...base, data: new Uint8Array(0) })).toThrowError('EMPTY_FRAME');
  });

  it('lanza con data > 255×1100 B (no entra en uint8 totalChunks)', () => {
    expect(() => chunkFrame({ ...base, data: new Uint8Array(255 * MAX_PAYLOAD + 1) })).toThrowError(
      'TOO_MANY_CHUNKS',
    );
  });

  it('lanza con maxPayload fuera de rango', () => {
    expect(() => chunkFrame({ ...base, data: makeData(10), maxPayload: 0 })).toThrowError(
      'INVALID_MAX_PAYLOAD',
    );
    expect(() => chunkFrame({ ...base, data: makeData(10), maxPayload: 1101 })).toThrowError(
      'INVALID_MAX_PAYLOAD',
    );
  });
});
