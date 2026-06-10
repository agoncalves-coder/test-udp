// Orquestación de UI (PRD §3): idle → connecting → countdown(1s) →
// capturing(3s) → uploading → results. El loop de captura y el transporte
// viven FUERA de React (capture/, transport/); acá solo estado de pantalla.
import { useCallback, useEffect, useRef, useState } from 'react';
import { createSession, getExperiments, postClientStats } from './api/client';
import { startCamera, type CameraHandle, type FacingMode } from './capture/camera';
import { runCapture, type CaptureHandle, type CaptureResult } from './capture/captureLoop';
import { pickEncoder } from './capture/encoders';
import { detectFeatures, type Features } from './capture/features';
import { CameraView } from './components/CameraView';
import { ExperimentPicker } from './components/ExperimentPicker';
import { Results } from './components/Results';
import type { Preset } from './experiments/types';
import { connectTransport } from './transport/connect';
import { PacedSender } from './transport/sender';

type Phase = 'idle' | 'connecting' | 'countdown' | 'capturing' | 'uploading' | 'results';

const CAPTURE_MS = 3000;
const COUNTDOWN_MS = 1000;
const PROGRESS_THROTTLE_MS = 500; // ≤2 updates/s hacia React (PRD §12.5)

export default function App() {
  const videoRef = useRef<HTMLVideoElement>(null);
  const cameraRef = useRef<CameraHandle | null>(null);
  const captureRef = useRef<CaptureHandle | null>(null);
  const lastProgressRef = useRef(0);

  const [phase, setPhase] = useState<Phase>('idle');
  const [error, setError] = useState('');
  const [presets, setPresets] = useState<Preset[]>([]);
  const [features, setFeatures] = useState<Features | null>(null);
  const [presetId, setPresetId] = useState('E2-gray-mid');
  const [facing, setFacing] = useState<FacingMode>('user');
  const [progress, setProgress] = useState({ framesCaptured: 0, framesSkipped: 0 });
  const [sessionId, setSessionId] = useState('');
  const [channel, setChannel] = useState('');
  const [captureResult, setCaptureResult] = useState<CaptureResult | null>(null);

  // Bootstrap: features + presets del backend.
  useEffect(() => {
    detectFeatures().then(setFeatures);
    getExperiments()
      .then(setPresets)
      .catch(() => setError('No se pudo contactar el backend. ¿Está corriendo?'));
  }, []);

  // Cámara: arranca al montar y al cambiar selfie/trasera (PRD: conmutables).
  useEffect(() => {
    const video = videoRef.current;
    if (!video || phase === 'results') return;
    let active = true;
    startCamera(video, facing)
      .then((cam) => {
        if (!active) {
          cam.stop();
          return;
        }
        cameraRef.current = cam;
      })
      .catch((e) => setError(`Cámara: ${e instanceof Error ? e.message : e}`));
    return () => {
      active = false;
      cameraRef.current?.stop();
      cameraRef.current = null;
    };
  }, [facing, phase]);

  const capture = useCallback(async () => {
    const video = videoRef.current;
    const preset = presets.find((p) => p.id === presetId);
    if (!video || !preset || !features) return;
    setError('');
    setCaptureResult(null);

    try {
      // 1. Sesión + canal (PRD §3.2-3.3).
      setPhase('connecting');
      const created = await createSession(preset.id);
      setSessionId(created.sessionId);
      const { transport, fallbackReason } = await connectTransport({
        sessionId: created.sessionId,
        stunUrl: created.stunUrl,
        forceWebSocket: preset.forceWebSocket,
      });
      setChannel(transport.kind);

      // 2. Cuenta regresiva de 1s (PRD §3.4).
      setPhase('countdown');
      await new Promise((r) => setTimeout(r, COUNTDOWN_MS));

      // 3. Captura de 3s exactos; el loop corre fuera de React.
      setPhase('capturing');
      setProgress({ framesCaptured: 0, framesSkipped: 0 });
      const sender = new PacedSender(transport, { bitrateBps: preset.bitrateKbps * 1000 });
      const handle = runCapture({
        preset,
        sessionSeq: created.sessionSeq,
        sessionId: created.sessionId,
        video,
        sender,
        encoder: pickEncoder(preset, features),
        durationMs: CAPTURE_MS,
        onProgress: (p) => {
          const now = Date.now();
          if (now - lastProgressRef.current >= PROGRESS_THROTTLE_MS) {
            lastProgressRef.current = now;
            setProgress(p);
          }
        },
      });
      captureRef.current = handle;
      const result = await handle.done;
      setCaptureResult(result);

      // 4. Stats del cliente por HTTP confiable (señal de fin autoritativa).
      setPhase('uploading');
      const cam = cameraRef.current;
      await postClientStats(created.sessionId, {
        userAgent: navigator.userAgent,
        presetId: preset.id,
        requestedRes: '320x240',
        actualRes: cam ? `${cam.actualWidth}x${cam.actualHeight}` : 'unknown',
        fpsTarget: preset.fps,
        fpsActual: result.fpsActual,
        encodeMsAvg: result.encodeMsAvg,
        framesSent: result.framesCaptured,
        framesSkipped: result.framesSkipped,
        framesDropped: result.framesDropped,
        chunksSent: result.chunksSent,
        channel: transport.kind,
        fallbackReason: fallbackReason ?? '',
        features,
      }).catch(() => {
        // el END_OF_CAPTURE por el canal ya disparó la consolidación
      });
      transport.close();

      setPhase('results');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      captureRef.current?.cancel();
      setPhase('idle');
    }
  }, [presets, presetId, features]);

  const reset = useCallback(() => {
    setPhase('idle');
    setSessionId('');
    setChannel('');
    setCaptureResult(null);
  }, []);

  const busy = phase !== 'idle' && phase !== 'results';

  return (
    <main>
      <h1>face-capture-poc</h1>
      {error && <p className="error">{error}</p>}

      {phase !== 'results' && (
        <>
          <CameraView
            ref={videoRef}
            facing={facing}
            disabled={busy}
            onToggleFacing={() => setFacing((f) => (f === 'user' ? 'environment' : 'user'))}
          />
          {features && (
            <ExperimentPicker
              presets={presets}
              selected={presetId}
              features={features}
              disabled={busy}
              onSelect={setPresetId}
            />
          )}
          <button
            type="button"
            className="capture"
            onClick={capture}
            disabled={busy || presets.length === 0 || !features}
          >
            {phase === 'idle' && 'Capturar 3s'}
            {phase === 'connecting' && 'Conectando…'}
            {phase === 'countdown' && '¡Preparate!'}
            {phase === 'capturing' &&
              `Capturando… ${progress.framesCaptured} frames (${progress.framesSkipped} salteados)`}
            {phase === 'uploading' && 'Enviando…'}
          </button>
          {channel && busy && <p className="channel">canal: {channel}</p>}
        </>
      )}

      {phase === 'results' && sessionId && (
        <Results sessionId={sessionId} channel={channel} capture={captureResult} onReset={reset} />
      )}
    </main>
  );
}
