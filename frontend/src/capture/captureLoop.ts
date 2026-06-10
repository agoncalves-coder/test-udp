// El loop de captura vive ENTERO fuera de React (PRD §12.5): React solo lo
// arranca desde un click handler y recibe progreso throttled. setInterval, no
// requestAnimationFrame (en gama baja rAF es errático y acopla captura a render).
import type { Preset } from '../experiments/types';
import { Flag } from '../protocol/header';
import { chunkFrame } from '../transport/chunker';
import type { PacedSender } from '../transport/sender';
import type { FrameEncoder } from './encoders';

export interface CaptureConfig {
  preset: Preset;
  sessionSeq: number;
  sessionId: string;
  video: HTMLVideoElement;
  sender: PacedSender;
  encoder: FrameEncoder;
  durationMs: number; // PRD: 3000 exactos
  onProgress?: (p: { framesCaptured: number; framesSkipped: number }) => void;
}

export interface CaptureResult {
  framesCaptured: number;
  framesSkipped: number;
  framesDropped: number;
  chunksSent: number;
  encodeMsAvg: number;
  fpsActual: number;
  codecUsed: number;
  startedAt: number;
  endedAt: number;
}

export interface CaptureHandle {
  cancel(): void;
  done: Promise<CaptureResult>;
}

const END_OF_CAPTURE_REPEATS = 3;
const END_OF_CAPTURE_SPACING_MS = 150;

export function runCapture(cfg: CaptureConfig): CaptureHandle {
  const { preset, video, sender, encoder } = cfg;
  const tickMs = 1000 / preset.fps;

  let frameId = 0;
  let framesSkipped = 0;
  let encodeMsTotal = 0;
  let codecUsed = preset.codec;
  let busy = false;
  let cancelled = false;
  let pendingEncode: Promise<void> = Promise.resolve();

  const startedAt = Date.now();

  const tick = () => {
    if (cancelled) {
      return;
    }
    // Si el encode anterior sigue en vuelo, NUNCA encolar: saltear (PRD §5).
    if (busy) {
      framesSkipped++;
      cfg.onProgress?.({ framesCaptured: frameId, framesSkipped });
      return;
    }
    busy = true;
    const thisFrameId = frameId++;
    const now = Date.now();
    // Mejor esfuerzo: el último tick que entra en la ventana marca lastFrame.
    const isLast = now + tickMs >= startedAt + cfg.durationMs;
    const t0 = performance.now();

    pendingEncode = encoder
      .encode(video)
      .then((encoded) => {
        encodeMsTotal += performance.now() - t0;
        if (!encoded || cancelled) {
          if (!encoded) {
            framesSkipped++;
          }
          return;
        }
        codecUsed = encoded.codec;
        const chunks = chunkFrame({
          sessionSeq: cfg.sessionSeq,
          frameId: thisFrameId,
          codec: encoded.codec,
          flags: encoded.flags | (isLast ? Flag.LastFrame : 0),
          data: encoded.data,
        });
        sender.enqueueFrame(chunks);
        cfg.onProgress?.({ framesCaptured: frameId, framesSkipped });
      })
      .catch(() => {
        framesSkipped++;
      })
      .finally(() => {
        busy = false;
      });
  };

  const interval = setInterval(tick, tickMs);
  tick(); // primer frame inmediato: 3s de captura = 3s de contenido

  const done = new Promise<CaptureResult>((resolve) => {
    const finish = async () => {
      clearInterval(interval);
      await pendingEncode; // encode en vuelo
      await sender.flushAndStop(); // drenar al ritmo del pacing
      const endedAt = Date.now();

      // END_OF_CAPTURE 3× espaciado: el canal unreliable puede perderlo y el
      // backend lo trata como idempotente (PRD §4).
      const framesCaptured = frameId;
      for (let i = 0; i < END_OF_CAPTURE_REPEATS; i++) {
        try {
          sender.transport.sendControl({
            type: 'end_of_capture',
            sessionId: cfg.sessionId,
            framesSent: framesCaptured,
            chunksSent: sender.stats.chunksSent,
            captureStartMs: startedAt,
            captureEndMs: endedAt,
          });
        } catch {
          break; // canal caído: client-stats por HTTP actúa de señal de fin
        }
        if (i < END_OF_CAPTURE_REPEATS - 1) {
          await new Promise((r) => setTimeout(r, END_OF_CAPTURE_SPACING_MS));
        }
      }

      encoder.close();
      const elapsed = (endedAt - startedAt) / 1000;
      resolve({
        framesCaptured,
        framesSkipped,
        framesDropped: sender.stats.framesDropped,
        chunksSent: sender.stats.chunksSent,
        encodeMsAvg: framesCaptured > 0 ? encodeMsTotal / framesCaptured : 0,
        fpsActual: elapsed > 0 ? framesCaptured / elapsed : 0,
        codecUsed,
        startedAt,
        endedAt,
      });
    };

    setTimeout(finish, cfg.durationMs);
  });

  return {
    cancel() {
      cancelled = true;
      clearInterval(interval);
      sender.abort();
      encoder.close();
    },
    done,
  };
}
