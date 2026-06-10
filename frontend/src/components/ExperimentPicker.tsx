import type { Preset } from '../experiments/types';
import type { Features } from '../capture/features';
import { presetSupported } from '../capture/features';

interface Props {
  presets: Preset[];
  selected: string;
  features: Features;
  disabled: boolean;
  onSelect: (id: string) => void;
}

export function ExperimentPicker({ presets, selected, features, disabled, onSelect }: Props) {
  const visible = presets.filter((p) => presetSupported(p.requiresFeature, features));
  const current = visible.find((p) => p.id === selected);

  return (
    <div className="picker">
      <label htmlFor="experiment">Experimento</label>
      <select
        id="experiment"
        value={selected}
        disabled={disabled}
        onChange={(e) => onSelect(e.target.value)}
      >
        {visible.map((p) => (
          <option key={p.id} value={p.id}>
            {p.id} — {p.label}
          </option>
        ))}
      </select>
      {current?.wifiOnly && (
        <p className="warn">⚠️ Solo WiFi: ~{Math.round(current.bitrateKbps / 100) / 10} Mbps. En 3G los resultados serían ruido.</p>
      )}
    </div>
  );
}
