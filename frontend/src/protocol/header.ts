// Contrato binario del protocolo de chunking (PRD §4).
// Header de 12 bytes big-endian:
//   magic u16 (0xFACE) | sessionSeq u16 | frameId u16 | chunkIndex u8 |
//   totalChunks u8 | payloadLen u16 | flags u8 | codec u8
// Espejo del lado Go (backend/internal/protocol). Los vectores golden viven en
// shared/protocol-golden.json y son la única fuente de verdad del contrato.

export const MAGIC = 0xface;
export const HEADER_SIZE = 12;
export const MAX_PAYLOAD = 1100;

// Nota: `erasableSyntaxOnly` (tsconfig del scaffold) prohíbe `enum`; este patrón
// const+type es el equivalente erasable con el mismo uso: `Codec.JPEG` como
// valor y `Codec` como tipo.
export const Codec = {
  JPEG: 0,
  WebP: 1,
  RawGray: 2,
  H264: 3,
} as const;
export type Codec = (typeof Codec)[keyof typeof Codec];

export const Flag = {
  Grayscale: 1,
  Keyframe: 2,
  LastFrame: 4,
} as const;
export type Flag = (typeof Flag)[keyof typeof Flag];

export interface ChunkHeader {
  sessionSeq: number;
  frameId: number;
  chunkIndex: number;
  totalChunks: number;
  payloadLen: number;
  flags: number;
  codec: number;
}

function isU16(n: number): boolean {
  return Number.isInteger(n) && n >= 0 && n <= 0xffff;
}

function isU8(n: number): boolean {
  return Number.isInteger(n) && n >= 0 && n <= 0xff;
}

// Validación compartida por encode y decode (idéntica al lado Go):
// totalChunks >= 1, chunkIndex < totalChunks, payloadLen <= 1100, codec <= 3,
// más rangos uint16/uint8 (en TS un number fuera de rango se truncaría en
// silencio al escribir el DataView).
function validateHeader(h: ChunkHeader): void {
  if (
    !isU16(h.sessionSeq) ||
    !isU16(h.frameId) ||
    !isU8(h.chunkIndex) ||
    !isU8(h.totalChunks) ||
    !isU8(h.flags) ||
    h.totalChunks < 1 ||
    h.chunkIndex >= h.totalChunks ||
    !isU16(h.payloadLen) ||
    h.payloadLen > MAX_PAYLOAD ||
    !Number.isInteger(h.codec) ||
    h.codec < 0 ||
    h.codec > 3
  ) {
    throw new Error('INVALID_HEADER');
  }
}

export function encodeChunk(
  h: Omit<ChunkHeader, 'payloadLen'>,
  payload: Uint8Array,
): Uint8Array {
  const header: ChunkHeader = { ...h, payloadLen: payload.length };
  validateHeader(header);

  const out = new Uint8Array(HEADER_SIZE + payload.length);
  const view = new DataView(out.buffer);
  // littleEndian=false explícito en cada acceso multi-byte: el wire format es big-endian.
  view.setUint16(0, MAGIC, false);
  view.setUint16(2, header.sessionSeq, false);
  view.setUint16(4, header.frameId, false);
  view.setUint8(6, header.chunkIndex);
  view.setUint8(7, header.totalChunks);
  view.setUint16(8, header.payloadLen, false);
  view.setUint8(10, header.flags);
  view.setUint8(11, header.codec);
  out.set(payload, HEADER_SIZE);
  return out;
}

export function decodeChunk(buf: ArrayBuffer | Uint8Array): {
  header: ChunkHeader;
  payload: Uint8Array;
} {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  if (bytes.length < HEADER_SIZE) {
    throw new Error('SHORT_HEADER');
  }
  // byteOffset/byteLength explícitos: `bytes` puede ser un subarray de un buffer mayor.
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  if (view.getUint16(0, false) !== MAGIC) {
    throw new Error('BAD_MAGIC');
  }
  const header: ChunkHeader = {
    sessionSeq: view.getUint16(2, false),
    frameId: view.getUint16(4, false),
    chunkIndex: view.getUint8(6),
    totalChunks: view.getUint8(7),
    payloadLen: view.getUint16(8, false),
    flags: view.getUint8(10),
    codec: view.getUint8(11),
  };
  if (bytes.length !== HEADER_SIZE + header.payloadLen) {
    throw new Error('LENGTH_MISMATCH');
  }
  validateHeader(header);
  // Payload zero-copy: subslice sobre el buffer recibido.
  return { header, payload: bytes.subarray(HEADER_SIZE) };
}
