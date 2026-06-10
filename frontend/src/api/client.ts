// Cliente REST del backend: sesiones, experimentos, resultados.
import type { Preset } from '../experiments/types';

export const apiBase: string =
  (import.meta.env.VITE_API_URL as string | undefined) ?? 'http://localhost:8080';

export interface CreatedSession {
  sessionId: string;
  sessionSeq: number;
  preset: Preset;
  stunUrl: string;
}

export interface PartialFrame {
  frameId: number;
  receivedPct: number;
}

// Espejo de reassembler.Report (PRD §7).
export interface Report {
  framesExpected: number;
  framesComplete: number;
  framesPartial: number;
  framesLost: number;
  partials: PartialFrame[];
  chunksReceived: number;
  chunksDuplicate: number;
  chunksLate: number;
  chunksWrongSession: number;
  protocolErrors: number;
  decodeErrors: number;
  bytesReceived: number;
  effectiveBitrateBps: number;
  latencyP50Ms: number;
  latencyP95Ms: number;
  totalMs: number;
  framesSentByClient: number;
}

export interface SessionState {
  sessionId: string;
  presetId: string;
  state: 'open' | 'capturing' | 'ending' | 'done';
  transport: string;
  live: {
    framesComplete: number;
    framesPending: number;
    chunksReceived: number;
    bytesReceived: number;
  };
  report?: Report;
  decodeSkipped?: boolean;
}

export interface FrameEntry {
  frameId: number;
  state: 'complete' | 'partial';
  receivedPct: number;
  url?: string;
}

async function check(res: Response): Promise<Response> {
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} en ${res.url}`);
  }
  return res;
}

export async function getExperiments(): Promise<Preset[]> {
  const res = await check(await fetch(`${apiBase}/experiments`));
  return res.json();
}

export async function createSession(presetId: string): Promise<CreatedSession> {
  const res = await check(
    await fetch(`${apiBase}/sessions`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ presetId }),
    }),
  );
  return res.json();
}

export async function getSession(id: string): Promise<SessionState> {
  const res = await check(await fetch(`${apiBase}/sessions/${id}`));
  return res.json();
}

export async function getFrames(id: string): Promise<FrameEntry[]> {
  const res = await check(await fetch(`${apiBase}/sessions/${id}/frames`));
  return res.json();
}

export function frameUrl(path: string): string {
  return `${apiBase}${path}`;
}

export async function postClientStats(id: string, stats: object): Promise<void> {
  await check(
    await fetch(`${apiBase}/sessions/${id}/client-stats`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(stats),
    }),
  );
}

// URL del WebSocket de fallback, derivada del API base (https → wss).
export function wsUrl(sessionId: string): string {
  const u = new URL(apiBase);
  u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:';
  u.pathname = '/ws';
  u.search = `?session=${encodeURIComponent(sessionId)}`;
  return u.toString();
}
