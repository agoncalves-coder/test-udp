// E9: plano de luminancia crudo, cero encode (PRD §6) — aísla si el cuello de
// botella en gama baja es el encode JPEG o el transporte.
//
// Desviación deliberada del sketch del PRD: un VideoFrame creado desde canvas
// es RGBA (no I420) y copyTo no tiene formato destino gris, así que extraer
// "solo el plano Y" por esa vía no existe. El camino robusto y universal es
// drawImage → getImageData → loop de luma entera: sub-milisegundo a 160×120,
// que es exactamente el punto diagnóstico de E9 (encodeMs ≈ 0).
import type { Preset } from '../experiments/types';
import { Codec, Flag } from '../protocol/header';
import type { EncodedFrame, FrameEncoder } from './encoders';

export class RawGrayEncoder implements FrameEncoder {
  private readonly canvas: HTMLCanvasElement;
  private readonly ctx: CanvasRenderingContext2D;
  private closed = false;

  constructor(preset: Preset) {
    this.canvas = document.createElement('canvas');
    this.canvas.width = preset.width;
    this.canvas.height = preset.height;
    const ctx = this.canvas.getContext('2d', { willReadFrequently: true });
    if (!ctx) {
      throw new Error('canvas 2d no disponible');
    }
    this.ctx = ctx;
  }

  encode(video: HTMLVideoElement): Promise<EncodedFrame | null> {
    if (this.closed) {
      return Promise.resolve(null);
    }
    const { width: w, height: h } = this.canvas;
    this.ctx.drawImage(video, 0, 0, w, h);
    const px = this.ctx.getImageData(0, 0, w, h).data;

    // El buffer ES la imagen (w*h bytes); el backend lo levanta directo con
    // image.Gray, sin ffmpeg.
    const out = new Uint8Array(w * h);
    for (let i = 0, j = 0; j < out.length; i += 4, j++) {
      // Luma BT.601 entera: (77R + 150G + 29B) >> 8
      out[j] = (77 * px[i] + 150 * px[i + 1] + 29 * px[i + 2]) >> 8;
    }
    return Promise.resolve({ data: out, codec: Codec.RawGray, flags: Flag.Grayscale });
  }

  close(): void {
    this.closed = true;
  }
}
