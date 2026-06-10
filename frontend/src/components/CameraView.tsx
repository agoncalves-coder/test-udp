import { forwardRef } from 'react';
import type { FacingMode } from '../capture/camera';

interface Props {
  facing: FacingMode;
  disabled: boolean;
  onToggleFacing: () => void;
}

// El <video> es la fuente del capture loop: playsInline evita fullscreen en
// Android, muted+autoPlay permiten reproducir sin gesto adicional.
export const CameraView = forwardRef<HTMLVideoElement, Props>(function CameraView(
  { facing, disabled, onToggleFacing },
  ref,
) {
  return (
    <div className="camera">
      <video
        ref={ref}
        playsInline
        muted
        autoPlay
        className={facing === 'user' ? 'mirrored' : ''}
      />
      <button type="button" className="flip" onClick={onToggleFacing} disabled={disabled}>
        {facing === 'user' ? 'Cámara trasera' : 'Cámara selfie'}
      </button>
    </div>
  );
});
