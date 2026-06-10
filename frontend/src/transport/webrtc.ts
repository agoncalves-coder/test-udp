// Canal principal (PRD §2): RTCDataChannel {ordered:false, maxRetransmits:0}
// — semántica de datagrama sobre UDP real, lo más cercano a UDP que existe en
// la web. Señalización non-trickle: un solo POST /signal con la offer completa.
import { apiBase } from '../api/client';
import type { ChunkTransport, ControlMessage } from './types';

export interface WebRTCOptions {
  sessionId: string;
  stunUrl: string;
  /** Tope para el gathering ICE local; pasado esto se manda lo que haya. */
  gatherTimeoutMs?: number;
}

export async function connectWebRTC(opts: WebRTCOptions): Promise<ChunkTransport> {
  const pc = new RTCPeerConnection({
    iceServers: [{ urls: opts.stunUrl }],
  });

  try {
    const dc = pc.createDataChannel('data', {
      ordered: false,
      maxRetransmits: 0,
    });
    dc.binaryType = 'arraybuffer';

    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    // El candidato srflx vía STUN suele llegar en <500ms; 1.5s de tope evita
    // que un STUN bloqueado consuma todo el presupuesto de los 4s del race.
    await waitIceGathering(pc, opts.gatherTimeoutMs ?? 1500);

    const res = await fetch(`${apiBase}/signal`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId: opts.sessionId, offer: pc.localDescription }),
    });
    if (!res.ok) {
      throw new Error(`signal HTTP ${res.status}`);
    }
    const { answer } = (await res.json()) as { answer: RTCSessionDescriptionInit };
    await pc.setRemoteDescription(answer);

    await waitChannelOpen(dc);

    return {
      kind: 'webrtc',
      get bufferedAmount() {
        return dc.bufferedAmount;
      },
      send(chunk: Uint8Array) {
        dc.send(chunk as Uint8Array<ArrayBuffer>);
      },
      sendControl(msg: ControlMessage) {
        dc.send(JSON.stringify(msg));
      },
      close() {
        dc.close();
        pc.close();
      },
      async stats() {
        return summarizeStats(await pc.getStats());
      },
    };
  } catch (e) {
    pc.close();
    throw e;
  }
}

// Resumen compacto de getStats() para client-stats (PRD §7): par de candidatos
// activo (RTT, tipos) y contadores del DataChannel.
function summarizeStats(report: RTCStatsReport): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  report.forEach((s) => {
    const stat = s as Record<string, unknown>;
    if (stat.type === 'candidate-pair' && stat.state === 'succeeded' && stat.nominated) {
      out.rttMs =
        typeof stat.currentRoundTripTime === 'number' ? stat.currentRoundTripTime * 1000 : null;
      out.availableOutgoingBitrate = stat.availableOutgoingBitrate ?? null;
      out.pairBytesSent = stat.bytesSent ?? null;
    }
    if (stat.type === 'local-candidate' && stat.candidateType) {
      out.localCandidateType = stat.candidateType;
    }
    if (stat.type === 'data-channel') {
      out.dcMessagesSent = stat.messagesSent ?? null;
      out.dcBytesSent = stat.bytesSent ?? null;
    }
  });
  return out;
}

function waitIceGathering(pc: RTCPeerConnection, timeoutMs: number): Promise<void> {
  if (pc.iceGatheringState === 'complete') {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    const timer = setTimeout(finish, timeoutMs);
    function finish() {
      clearTimeout(timer);
      pc.removeEventListener('icegatheringstatechange', onChange);
      resolve();
    }
    function onChange() {
      if (pc.iceGatheringState === 'complete') {
        finish();
      }
    }
    pc.addEventListener('icegatheringstatechange', onChange);
  });
}

function waitChannelOpen(dc: RTCDataChannel): Promise<void> {
  if (dc.readyState === 'open') {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    dc.onopen = () => resolve();
    dc.onerror = (ev) => reject(new Error(`datachannel error: ${String(ev)}`));
    dc.onclose = () => reject(new Error('datachannel cerrado antes de abrir'));
  });
}
