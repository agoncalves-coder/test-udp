// Feature detection al inicio (PRD §6/§10): decide qué presets se muestran y
// alimenta client-stats — el % de dispositivos target con cada capability es
// en sí mismo un resultado del PoC.

export interface Features {
  /** canvas.toBlob produce WebP real (Chrome Android sí; otros caen a PNG/JPEG). */
  webp: boolean;
  /** ctx.filter = 'grayscale(1)' soportado (si no: loop ImageData, más caro). */
  grayscaleFilter: boolean;
  /** VideoEncoder H.264 disponible: por hardware, solo software, o ausente. */
  webcodecsH264: 'hw' | 'sw' | 'none';
  /** MediaStreamTrackProcessor disponible (dato de campo; E9 no lo necesita). */
  mstp: boolean;
}

export async function detectFeatures(): Promise<Features> {
  return {
    webp: detectWebP(),
    grayscaleFilter: detectGrayscaleFilter(),
    webcodecsH264: await detectWebCodecsH264(),
    mstp: 'MediaStreamTrackProcessor' in globalThis,
  };
}

function detectWebP(): boolean {
  try {
    const canvas = document.createElement('canvas');
    canvas.width = 2;
    canvas.height = 2;
    return canvas.toDataURL('image/webp').startsWith('data:image/webp');
  } catch {
    return false;
  }
}

function detectGrayscaleFilter(): boolean {
  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d');
  if (!ctx || typeof ctx.filter !== 'string') {
    return false;
  }
  ctx.filter = 'grayscale(1)';
  return ctx.filter === 'grayscale(1)';
}

// isConfigSupported con prefer-hardware es un requisito DURO en Chrome; si
// falla se reintenta con no-preference (encoder por software, dato distinto
// pero válido). Config espejo de E8 (PRD §6).
async function detectWebCodecsH264(): Promise<'hw' | 'sw' | 'none'> {
  if (typeof VideoEncoder === 'undefined' || !VideoEncoder.isConfigSupported) {
    return 'none';
  }
  const base: VideoEncoderConfig = {
    codec: 'avc1.42001f', // H.264 Baseline: máxima compatibilidad de decode
    width: 320,
    height: 240,
    bitrate: 400_000,
    framerate: 10,
    avc: { format: 'annexb' },
    latencyMode: 'realtime',
  };
  try {
    const hw = await VideoEncoder.isConfigSupported({
      ...base,
      hardwareAcceleration: 'prefer-hardware',
    });
    if (hw.supported) {
      return 'hw';
    }
    const sw = await VideoEncoder.isConfigSupported({
      ...base,
      hardwareAcceleration: 'no-preference',
    });
    if (sw.supported) {
      return 'sw';
    }
  } catch {
    // implementaciones viejas tiran en vez de devolver supported:false
  }
  return 'none';
}

/** ¿El preset puede ofrecerse en este dispositivo? */
export function presetSupported(requiresFeature: string | undefined, f: Features): boolean {
  switch (requiresFeature) {
    case undefined:
      return true;
    case 'webcodecs-h264':
      return f.webcodecsH264 !== 'none';
    case 'mstp':
      return f.mstp;
    default:
      return false;
  }
}
