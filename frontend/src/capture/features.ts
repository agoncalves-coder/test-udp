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
  /** MediaStreamTrackProcessor disponible (E9 fast-path). */
  mstp: boolean;
}

export async function detectFeatures(): Promise<Features> {
  return {
    webp: detectWebP(),
    grayscaleFilter: detectGrayscaleFilter(),
    // TODO(F3): VideoEncoder.isConfigSupported con prefer-hardware → retry
    // no-preference; hasta entonces E8/E9 quedan ocultos del selector.
    webcodecsH264: 'none',
    mstp: false,
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
