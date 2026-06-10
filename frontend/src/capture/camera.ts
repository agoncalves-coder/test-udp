// getUserMedia con constraints conservadores (PRD §5): pedir poco para que
// gama baja no muera. La resolución real obtenida puede diferir de la pedida;
// se registra y el downscale del canvas normaliza.

export type FacingMode = 'user' | 'environment';

export interface CameraHandle {
  stream: MediaStream;
  facing: FacingMode;
  /** Resolución real entregada por el dispositivo (track settings). */
  actualWidth: number;
  actualHeight: number;
  stop(): void;
}

export async function startCamera(
  video: HTMLVideoElement,
  facing: FacingMode,
): Promise<CameraHandle> {
  const stream = await navigator.mediaDevices.getUserMedia({
    video: {
      facingMode: facing,
      width: { ideal: 320 },
      height: { ideal: 240 },
      frameRate: { ideal: 15, max: 15 },
    },
    audio: false,
  });

  video.srcObject = stream;
  await video.play();

  const settings = stream.getVideoTracks()[0]?.getSettings() ?? {};
  return {
    stream,
    facing,
    actualWidth: settings.width ?? video.videoWidth,
    actualHeight: settings.height ?? video.videoHeight,
    stop() {
      stream.getTracks().forEach((t) => t.stop());
      video.srcObject = null;
    },
  };
}
