import { useEffect, useState } from 'react';
import {
  frameUrl,
  getFrames,
  getSession,
  type FrameEntry,
  type SessionState,
} from '../api/client';
import type { CaptureResult } from '../capture/captureLoop';

interface Props {
  sessionId: string;
  channel: string;
  capture: CaptureResult | null;
  onReset: () => void;
}

const POLL_MS = 500;
const POLL_TIMEOUT_MS = 20_000;

// Pantalla de resultados (PRD §3.8): polling hasta consolidación, % de frames
// reconstruidos, latencias y thumbnails para validación visual en campo.
export function Results({ sessionId, channel, capture, onReset }: Props) {
  const [session, setSession] = useState<SessionState | null>(null);
  const [frames, setFrames] = useState<FrameEntry[]>([]);
  const [timedOut, setTimedOut] = useState(false);

  useEffect(() => {
    let stop = false;
    const startedAt = Date.now();

    const poll = async () => {
      while (!stop) {
        try {
          const s = await getSession(sessionId);
          if (stop) return;
          setSession(s);
          if (s.state === 'done') {
            setFrames(await getFrames(sessionId));
            return;
          }
        } catch {
          // backend transitorio: seguir intentando hasta timeout
        }
        if (Date.now() - startedAt > POLL_TIMEOUT_MS) {
          setTimedOut(true);
          return;
        }
        await new Promise((r) => setTimeout(r, POLL_MS));
      }
    };
    void poll();
    return () => {
      stop = true;
    };
  }, [sessionId]);

  if (timedOut) {
    return (
      <section className="results">
        <p className="warn">El backend no consolidó la sesión a tiempo.</p>
        <button type="button" onClick={onReset}>Volver</button>
      </section>
    );
  }

  const report = session?.report;
  if (!report) {
    return (
      <section className="results">
        <p>Consolidando… {session ? `(${session.live.framesComplete} frames, ${session.live.chunksReceived} chunks)` : ''}</p>
      </section>
    );
  }

  const pctComplete =
    report.framesExpected > 0
      ? Math.round((100 * report.framesComplete) / report.framesExpected)
      : 0;

  return (
    <section className="results">
      <h2>
        {report.framesComplete}/{report.framesExpected} frames ({pctComplete}%)
        <span className={pctComplete >= 70 ? 'ok' : 'fail'}>
          {pctComplete >= 70 ? ' ✓ criterio MVP' : ' ✗ bajo el criterio (70%)'}
        </span>
      </h2>

      <dl className="metrics">
        <dt>Canal</dt>
        <dd>{channel}</dd>
        <dt>Parciales / perdidos</dt>
        <dd>{report.framesPartial} / {report.framesLost}</dd>
        <dt>Latencia frame p50 / p95</dt>
        <dd>{report.latencyP50Ms.toFixed(0)} / {report.latencyP95Ms.toFixed(0)} ms</dd>
        <dt>Bitrate efectivo</dt>
        <dd>{(report.effectiveBitrateBps / 1000).toFixed(0)} kbps</dd>
        <dt>Chunks (dup / tardíos)</dt>
        <dd>{report.chunksReceived} ({report.chunksDuplicate} / {report.chunksLate})</dd>
        <dt>Errores de decode</dt>
        <dd>{report.decodeErrors}</dd>
        {capture && (
          <>
            <dt>Encode promedio</dt>
            <dd>{capture.encodeMsAvg.toFixed(1)} ms</dd>
            <dt>FPS real / salteados</dt>
            <dd>{capture.fpsActual.toFixed(1)} / {capture.framesSkipped}</dd>
          </>
        )}
      </dl>

      <div className="thumbs">
        {frames.map((f) =>
          f.url ? (
            <figure key={f.frameId}>
              <img src={frameUrl(f.url)} alt={`frame ${f.frameId}`} loading="lazy" />
              <figcaption>#{f.frameId}</figcaption>
            </figure>
          ) : (
            <figure key={f.frameId} className="partial">
              <div className="missing">{f.receivedPct.toFixed(0)}%</div>
              <figcaption>#{f.frameId} parcial</figcaption>
            </figure>
          ),
        )}
      </div>

      <button type="button" onClick={onReset}>Nueva captura</button>
    </section>
  );
}
