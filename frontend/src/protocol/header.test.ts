// Tests del contrato binario contra shared/protocol-golden.json (única fuente
// de verdad, compartida con backend/internal/protocol). Si este lado deriva del
// contrato, estos tests fallan.

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  Codec,
  decodeChunk,
  encodeChunk,
  Flag,
  HEADER_SIZE,
  MAGIC,
  MAX_PAYLOAD,
  type ChunkHeader,
} from './header';

interface GoldenValid {
  name: string;
  hex: string;
  fields: ChunkHeader;
  payloadHex: string;
}

interface GoldenInvalid {
  name: string;
  hex: string;
  error: string;
}

const golden = JSON.parse(
  readFileSync(new URL('../../../shared/protocol-golden.json', import.meta.url), 'utf8'),
) as { valid: GoldenValid[]; invalid: GoldenInvalid[] };

function hexToBytes(hex: string): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

function bytesToHex(bytes: Uint8Array): string {
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

describe('constantes del contrato', () => {
  it('coinciden con el PRD §4', () => {
    expect(MAGIC).toBe(0xface);
    expect(HEADER_SIZE).toBe(12);
    expect(MAX_PAYLOAD).toBe(1100);
    expect(Codec.JPEG).toBe(0);
    expect(Codec.WebP).toBe(1);
    expect(Codec.RawGray).toBe(2);
    expect(Codec.H264).toBe(3);
    expect(Flag.Grayscale).toBe(1);
    expect(Flag.Keyframe).toBe(2);
    expect(Flag.LastFrame).toBe(4);
  });
});

describe('golden: decode', () => {
  for (const v of golden.valid) {
    it(`decodifica ${v.name}`, () => {
      const { header, payload } = decodeChunk(hexToBytes(v.hex));
      expect(header).toEqual(v.fields);
      expect(bytesToHex(payload)).toBe(v.payloadHex);
    });

    it(`decodifica ${v.name} desde ArrayBuffer`, () => {
      const { header, payload } = decodeChunk(hexToBytes(v.hex).buffer);
      expect(header).toEqual(v.fields);
      expect(bytesToHex(payload)).toBe(v.payloadHex);
    });
  }

  it('decodifica un subarray con byteOffset != 0 (zero-copy sobre buffer mayor)', () => {
    const v = golden.valid[0];
    const raw = hexToBytes(v.hex);
    const padded = new Uint8Array(raw.length + 8);
    padded.set(raw, 8);
    const { header, payload } = decodeChunk(padded.subarray(8));
    expect(header).toEqual(v.fields);
    expect(bytesToHex(payload)).toBe(v.payloadHex);
  });
});

describe('golden: encode', () => {
  for (const v of golden.valid) {
    it(`codifica ${v.name} byte a byte`, () => {
      const { payloadLen, ...headerSinLen } = v.fields;
      const encoded = encodeChunk(headerSinLen, hexToBytes(v.payloadHex));
      expect(encoded.length).toBe(HEADER_SIZE + payloadLen);
      expect(bytesToHex(encoded)).toBe(v.hex);
    });

    it(`round-trip encode→decode de ${v.name}`, () => {
      const { payloadLen: _payloadLen, ...headerSinLen } = v.fields;
      const { header, payload } = decodeChunk(encodeChunk(headerSinLen, hexToBytes(v.payloadHex)));
      expect(header).toEqual(v.fields);
      expect(bytesToHex(payload)).toBe(v.payloadHex);
    });
  }
});

describe('golden: inválidos', () => {
  for (const v of golden.invalid) {
    it(`${v.name} lanza ${v.error}`, () => {
      expect(() => decodeChunk(hexToBytes(v.hex))).toThrowError(v.error);
    });
  }
});

describe('big-endian explícito en el wire', () => {
  it('sessionSeq=0x0102 queda byte[2]=0x01, byte[3]=0x02', () => {
    const encoded = encodeChunk(
      { sessionSeq: 0x0102, frameId: 0x0304, chunkIndex: 0, totalChunks: 1, flags: 0, codec: 0 },
      Uint8Array.of(0xaa),
    );
    expect(encoded[0]).toBe(0xfa); // magic MSB primero
    expect(encoded[1]).toBe(0xce);
    expect(encoded[2]).toBe(0x01); // sessionSeq MSB primero
    expect(encoded[3]).toBe(0x02);
    expect(encoded[4]).toBe(0x03); // frameId MSB primero
    expect(encoded[5]).toBe(0x04);
    expect(encoded[8]).toBe(0x00); // payloadLen=1 big-endian
    expect(encoded[9]).toBe(0x01);
  });
});

describe('validación en encode', () => {
  const base = { sessionSeq: 1, frameId: 1, chunkIndex: 0, totalChunks: 1, flags: 0, codec: 0 };
  const payload = Uint8Array.of(1, 2, 3);

  it('rechaza payload > MAX_PAYLOAD', () => {
    expect(() => encodeChunk(base, new Uint8Array(MAX_PAYLOAD + 1))).toThrowError(
      'INVALID_HEADER',
    );
  });

  it('acepta payload == MAX_PAYLOAD', () => {
    expect(encodeChunk(base, new Uint8Array(MAX_PAYLOAD)).length).toBe(HEADER_SIZE + MAX_PAYLOAD);
  });

  it('rechaza totalChunks == 0', () => {
    expect(() => encodeChunk({ ...base, totalChunks: 0 }, payload)).toThrowError('INVALID_HEADER');
  });

  it('rechaza chunkIndex >= totalChunks', () => {
    expect(() => encodeChunk({ ...base, chunkIndex: 1, totalChunks: 1 }, payload)).toThrowError(
      'INVALID_HEADER',
    );
  });

  it('rechaza codec > 3', () => {
    expect(() => encodeChunk({ ...base, codec: 4 }, payload)).toThrowError('INVALID_HEADER');
  });

  it('rechaza valores fuera de rango uint16/uint8', () => {
    expect(() => encodeChunk({ ...base, sessionSeq: 0x10000 }, payload)).toThrowError(
      'INVALID_HEADER',
    );
    expect(() => encodeChunk({ ...base, frameId: -1 }, payload)).toThrowError('INVALID_HEADER');
    expect(() => encodeChunk({ ...base, flags: 256 }, payload)).toThrowError('INVALID_HEADER');
  });
});

describe('validación en decode (más allá de los vectores golden)', () => {
  it('rechaza buffer vacío con SHORT_HEADER', () => {
    expect(() => decodeChunk(new Uint8Array(0))).toThrowError('SHORT_HEADER');
  });

  it('rechaza payloadLen > MAX_PAYLOAD aunque el largo del buffer coincida', () => {
    const oversized = encodeChunk(
      { sessionSeq: 1, frameId: 1, chunkIndex: 0, totalChunks: 1, flags: 0, codec: 0 },
      new Uint8Array(MAX_PAYLOAD),
    );
    const grown = new Uint8Array(HEADER_SIZE + MAX_PAYLOAD + 1);
    grown.set(oversized);
    const view = new DataView(grown.buffer);
    view.setUint16(8, MAX_PAYLOAD + 1, false); // largo del buffer == 12+payloadLen, pero > 1100
    expect(() => decodeChunk(grown)).toThrowError('INVALID_HEADER');
  });
});
