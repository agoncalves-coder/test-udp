// Encoders de frame (PRD §5): canvas.toBlob con JPEG/WebP. El canvas se crea
// UNA vez al tamaño objetivo del preset; el downscale lo hace drawImage.
// Nada de WASM ni workers (PRD: gama baja).
import type { Preset } from '../experiments/types';
import { Codec, Flag } from '../protocol/header';
import type { Features } from './features';

export interface EncodedFrame {
  data: Uint8Array;
  codec: number;
  flags: number;
}

export interface FrameEncoder {
  /** null: el browser no produjo blob (frame se saltea, no es error fatal). */
  encode(video: HTMLVideoElement): Promise<EncodedFrame | null>;
  close(): void;
}

export function pickEncoder(preset: Preset, features: Features): FrameEncoder {
  switch (preset.codec) {
    case Codec.JPEG:
      return new CanvasBlobEncoder(preset, features, 'image/jpeg');
    case Codec.WebP:
      // Si toBlob no produce WebP real, el encoder lo detecta por blob.type y
      // reporta codec=0 (riesgo del PRD §10).
      return new CanvasBlobEncoder(preset, features, 'image/webp');
    default:
      // E8 (WebCodecs) y E9 (raw gray) llegan en F3; el selector los oculta
      // mientras detectFeatures reporte que no están disponibles.
      throw new Error(`codec ${preset.codec} no implementado todavía`);
  }
}

class CanvasBlobEncoder implements FrameEncoder {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private readonly mime: string;
  private readonly quality: number;
  private readonly grayscale: boolean;
  private readonly useFilter: boolean;
  private closed = false;

  constructor(preset: Preset, features: Features, mime: string) {
    this.canvas = document.createElement('canvas');
    this.canvas.width = preset.width;
    this.canvas.height = preset.height;
    const ctx = this.canvas.getContext('2d', { willReadFrequently: false });
    if (!ctx) {
      throw new Error('canvas 2d no disponible');
    }
    this.ctx = ctx;
    this.mime = mime;
    this.quality = preset.quality;
    this.grayscale = preset.grayscale;
    this.useFilter = preset.grayscale && features.grayscaleFilter;
    if (this.useFilter) {
      this.ctx.filter = 'grayscale(1)';
    }
  }

  async encode(video: HTMLVideoElement): Promise<EncodedFrame | null> {
    if (this.closed) {
      return null;
    }
    this.ctx.drawImage(video, 0, 0, this.canvas.width, this.canvas.height);
    if (this.grayscale && !this.useFilter) {
      this.grayscaleImageData();
    }

    const blob = await new Promise<Blob | null>((resolve) =>
      this.canvas.toBlob(resolve, this.mime, this.quality),
    );
    if (!blob) {
      return null;
    }

    const data = new Uint8Array(await blob.arrayBuffer());
    // blob.type es la verdad: WebP no soportado cae a PNG/JPEG en silencio.
    const codec = blob.type === 'image/webp' ? Codec.WebP : Codec.JPEG;
    return {
      data,
      codec,
      flags: this.grayscale ? Flag.Grayscale : 0,
    };
  }

  close(): void {
    this.closed = true;
  }

  // Fallback cuando ctx.filter no existe. En gama baja puede ser caro: el
  // costo queda capturado en encodeMs y el tick se saltea si no llega (PRD §5).
  private grayscaleImageData(): void {
    const { width, height } = this.canvas;
    const img = this.ctx.getImageData(0, 0, width, height);
    const px = img.data;
    for (let i = 0; i < px.length; i += 4) {
      // Luma BT.601 entera: (77R + 150G + 29B) >> 8
      const y = (77 * px[i] + 150 * px[i + 1] + 29 * px[i + 2]) >> 8;
      px[i] = y;
      px[i + 1] = y;
      px[i + 2] = y;
    }
    this.ctx.putImageData(img, 0, 0);
  }
}
