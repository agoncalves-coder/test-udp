# face-capture-poc

PoC: captura facial de 3 s desde una web app → streaming tipo UDP (WebRTC DataChannel unreliable, fallback WebSocket) → reconstrucción de frames en backend Go. Ver el PRD para contexto completo.

> **Estado:** en construcción. Este README se completa en la Fase 4 con los runbooks de deploy (Fly.io + Vercel) y de prueba de campo.

## Estructura

- `shared/protocol-golden.json` — vectores golden del protocolo de chunking; contrato entre Go y TS (ambas suites de tests lo consumen).
- `backend/` — Go 1.26. `pion/webrtc/v4` + `coder/websocket`. Sin frameworks web (stdlib `net/http`).
- `frontend/` — Vite + React + TS. Sin UI kits; CSS plano. Bundle mínimo para gama baja.

## Dev local (Windows, sin Docker)

```powershell
# terminal 1
cd backend ; go run ./cmd/server        # http://localhost:8080
# terminal 2
cd frontend ; npm run dev               # http://localhost:5173
```

## Tests

```powershell
cd backend ; go test ./... -race
cd frontend ; npx vitest run
```

## Dependencias y justificación (PRD §12.4)

| Dependencia | Por qué |
|---|---|
| `github.com/pion/webrtc/v4` | DataChannel unreliable server-side; requerida por el PRD |
| `github.com/coder/websocket` | Fallback WS; sucesor mantenido de `nhooyr.io/websocket` (el PRD lista nhooyr) |
| `golang.org/x/image` | Validación de decode WebP (E5) |
| `vitest` (dev) | Tests del chunker/protocolo exigidos por el PRD |
