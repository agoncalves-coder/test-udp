// E8: encode H.264 por hardware vía WebCodecs (PRD §6). Cada frame es
// keyframe (independiente: compatible con el chunking y descarte del §4).
// VideoEncoder es push-based y FrameEncoder pull-based: el adapter mantiene
// una FIFO de resolvers — en latencyMode realtime cada encode(keyFrame:true)
// produce exactamente un output, en orden.
import type { Preset } from '../experiments/types';
import { Codec, Flag } from '../protocol/header';
import type { EncodedFrame, FrameEncoder } from './encoders';

type Pending = {
  resolve: (f: EncodedFrame | null) => void;
};

export class WebCodecsH264Encoder implements FrameEncoder {
  private readonly encoder: VideoEncoder;
  private readonly pending: Pending[] = [];
  private closed = false;

  constructor(preset: Preset, mode: 'hw' | 'sw') {
    this.encoder = new VideoEncoder({
      output: (chunk) => {
        const data = new Uint8Array(chunk.byteLength);
        chunk.copyTo(data);
        this.pending.shift()?.resolve({
          data,
          codec: Codec.H264,
          // Annex-B con SPS/PPS in-band: cada .h264 es autodecodificable.
          flags: Flag.Keyframe,
        });
      },
      error: () => {
        this.drain();
      },
    });
    this.encoder.configure({
      codec: 'avc1.42001f',
      width: preset.width,
      height: preset.height,
      bitrate: preset.bitrateKbps * 1000,
      framerate: preset.fps,
      avc: { format: 'annexb' },
      latencyMode: 'realtime',
      hardwareAcceleration: mode === 'hw' ? 'prefer-hardware' : 'no-preference',
    });
  }

  encode(video: HTMLVideoElement): Promise<EncodedFrame | null> {
    if (this.closed || this.encoder.state !== 'configured') {
      return Promise.resolve(null);
    }
    // timestamp en µs; close() inmediato tras encode para no agotar memoria
    // en gama baja (PRD §6).
    const frame = new VideoFrame(video, { timestamp: performance.now() * 1000 });
    try {
      this.encoder.encode(frame, { keyFrame: true });
    } catch {
      frame.close();
      return Promise.resolve(null);
    }
    frame.close();
    return new Promise<EncodedFrame | null>((resolve) => {
      this.pending.push({ resolve });
    });
  }

  close(): void {
    this.closed = true;
    try {
      this.encoder.close();
    } catch {
      // ya cerrado
    }
    this.drain();
  }

  private drain(): void {
    while (this.pending.length > 0) {
      this.pending.shift()?.resolve(null);
    }
  }
}
