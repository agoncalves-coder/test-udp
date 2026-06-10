# face-capture-poc

PoC: captura facial de 3 s desde una web app → streaming tipo UDP (WebRTC DataChannel `{ordered:false, maxRetransmits:0}`, fallback WebSocket) → reconstrucción de frames en backend Go. Criterio de éxito (PRD §1): en gama baja sobre 3G, **≥ 70% de frames completos con la cara distinguible en < 6 s totales**.

## Estructura

- `shared/protocol-golden.json` — vectores golden del protocolo de chunking (header binario 12 B, PRD §4). **Contrato entre Go y TS**: ambas suites de tests lo consumen; si un lado deriva, sus tests fallan.
- `backend/` — Go 1.26, stdlib `net/http` + `pion/webrtc/v4` + `coder/websocket`. Dockerfile y fly.toml incluidos.
- `frontend/` — Vite + React + TS. Sin UI kits, CSS plano, bundle mínimo (target: Android 7–10, 1–2 GB RAM).

## Dev local (Windows, sin Docker)

```powershell
# terminal 1
cd backend ; go run ./cmd/server        # http://localhost:8080
# terminal 2
cd frontend ; npm run dev               # http://localhost:5173  (getUserMedia funciona en localhost)
```

Tests:

```powershell
cd backend ; go vet ./... ; go test ./... -race
cd frontend ; npx tsc --noEmit ; npx vitest run
```

ffmpeg es opcional en dev: sin él, los frames H.264 de E8 se persisten como `.h264` y el report marca `decodeSkipped`. Para habilitar el decode local: `winget install Gyan.FFmpeg`.

## Probar con teléfonos reales en LAN (sin deploy, sin certificados)

1. Reglas de firewall (una vez, PowerShell como admin):
   ```powershell
   netsh advfirewall firewall add rule name="facepoc-tcp-8080" dir=in action=allow protocol=TCP localport=8080
   netsh advfirewall firewall add rule name="facepoc-tcp-5173" dir=in action=allow protocol=TCP localport=5173
   netsh advfirewall firewall add rule name="facepoc-udp-5004" dir=in action=allow protocol=UDP localport=5004
   ```
2. Teléfono por USB con depuración USB activada (`winget install Google.PlatformTools` para adb):
   ```powershell
   adb reverse tcp:5173 tcp:5173
   adb reverse tcp:8080 tcp:8080
   ```
   En el teléfono: Chrome → `http://localhost:5173`. Al ser `localhost`, es secure context → la cámara funciona sin certificados y sin mixed content. El UDP de WebRTC fluye directo por WiFi (Pion anuncia la IP LAN de esta PC). El teléfono debe estar en el mismo WiFi.
3. Sin cable USB (alternativa): `npm run dev -- --host`, y en el teléfono setear `chrome://flags/#unsafely-treat-insecure-origin-as-secure` a `http://<IP-LAN-de-la-PC>:5173` y relanzar Chrome.

### Simulación 3G válida

⚠️ **El throttling "Slow 3G" de Chrome DevTools NO afecta WebRTC** (sí afecta WebSockets desde Chrome ~99). Bajo DevTools, la comparación E2 (DataChannel) vs E7 (WS) es inválida: E7 quedaría throttled y E2 no. Usar DevTools solo para UX de señalización.

Simulación correcta: **[clumsy](https://jagt.github.io/clumsy/)** en esta PC (como admin), que shapea paquetes reales de ambos canales por igual:

- Filtro: `udp.DstPort == 5004 or udp.SrcPort == 5004 or tcp.DstPort == 8080 or tcp.SrcPort == 8080`
- Perfil "3G": Lag 150–400 ms, Drop 1–5%, bandwidth cap ~400 kbps.
- Test de fallback forzado: Drop 100% solo en `udp.DstPort == 5004` → la app debe caer a WebSocket en < 4 s.

Alternativa repetible: emulador Android con `-netspeed umts -netdelay umts -camera-back webcam0`.

### Runbook de prueba de campo (por dispositivo)

1. Preflight: abrir la app, captura de humo en WiFi limpio, confirmar `channel=webrtc` en resultados.
2. Correr la matriz: E1–E7 × 3 corridas (E8/E9 si el selector los muestra — dependen de feature detection), en (a) WiFi limpio, (b) clumsy-3G, (c) datos móviles reales.
3. Registrar por corrida: modelo, versión Android/Chrome, preset, red, sessionId, canal usado.
4. Recolectar `GET /sessions/:id/report.json` de cada corrida; `chrome://webrtc-internals` en el dispositivo para evidencia ICE.
5. Tabular contra el criterio: ≥ 70% frames completos, < 6 s total.

## Deploy a internet (runbook — ejecutar cuando se decida; ~USD 5-6/mes)

El backend necesita puertos UDP públicos para WebRTC → Vercel/Railway no sirven. Target: **Fly.io** (región `eze`). Costos: IPv4 dedicada $2/mes (obligatoria para UDP), máquina 24/7 ~$3-4/mes (UDP no permite scale-to-zero), volumen 1 GB $0.15/mes.

### Backend → Fly.io

```powershell
winget install Fly-io.flyctl
fly auth login                            # cuenta con tarjeta
cd backend
fly launch --no-deploy                    # responder NO a sobreescribir fly.toml
fly volumes create data --size 1 --region eze
fly ips allocate-v4 --yes                 # $2/mes — REQUERIDA para UDP
fly ips list                              # anotar la IPv4 dedicada
fly secrets set PUBLIC_IP=<ipv4-dedicada> ALLOWED_ORIGINS="https://<proyecto>.vercel.app,http://localhost:5173"
fly deploy --remote-only                  # build remoto, no requiere Docker local
fly scale count 1                         # señalización y UDP en la MISMA máquina
fly logs                                  # verificar bind de udp mux en fly-global-services:5004
```

Notas técnicas (ya implementadas en el código): con `FLY_APP_NAME` presente, el server bindea UDP a `fly-global-services:5004` y anuncia `PUBLIC_IP` vía `SetNAT1To1IPs` (candidato host). Sin `PUBLIC_IP`, resuelve el registro A de `<app>.fly.dev`. Fly no soporta rangos UDP → un único puerto con ICE UDPMux de Pion (en lugar del rango 50000–50100 que sugiere el PRD §2).

### Frontend → Vercel (integración GitHub)

1. vercel.com → Add New → Project → importar `face-capture-poc` (la cuenta de GitHub debe ser **personal**: Vercel Hobby no conecta repos de organización).
2. **Root Directory = `frontend`** (Vite se auto-detecta: `vite build` → `dist`).
3. Env var `VITE_API_URL = https://face-capture-poc.fly.dev` (Production + Preview). Se hornea en build time: cambiarla requiere redeploy.
4. Deploy → anotar la URL de producción → actualizar `ALLOWED_ORIGINS` en Fly (`fly secrets set ...`).
5. Smoke test desde un teléfono con datos móviles: captura E2 con `channel=webrtc` valida toda la cadena fly-global-services + UDPMux + NAT1To1.

Datos de campo: descargar `/data` con `fly ssh sftp get` tras cada jornada.

## Dependencias y justificación (PRD §12.4)

| Dependencia | Por qué |
|---|---|
| `github.com/pion/webrtc/v4` | DataChannel unreliable server-side; requerida por el PRD |
| `github.com/coder/websocket` | Fallback WS; sucesor mantenido de `nhooyr.io/websocket` (que lista el PRD) |
| `golang.org/x/image` | Validación de decode WebP (E5) |
| `vitest` (dev) | Tests del chunker/protocolo exigidos por el PRD §8 |

## Desviaciones del PRD

| PRD | Implementado | Motivo |
|---|---|---|
| Rango UDP 50000–50100 (`SetEphemeralUDPPortRange`) | Un puerto UDP (5004) + `ICEUDPMux` + `SetNAT1To1IPs` | Fly.io no proxya rangos UDP ni reescribe puertos destino; UDP exige IPv4 dedicada |
| `UDP_PORT_MIN`/`UDP_PORT_MAX` | `UDP_PORT` | Consecuencia del punto anterior |
| gorilla/websocket o nhooyr | `coder/websocket` | Sucesor mantenido de nhooyr |
| Validación con DevTools "Slow 3G" | clumsy / emulador Android | DevTools no throttlea WebRTC; la comparación E2-vs-E7 sería inválida |
| E9: `VideoFrame.copyTo` plano Y desde canvas | Loop de luma sobre `getImageData` (fast-path `copyTo` solo con I420/NV12 nativo) | Los VideoFrame con origen canvas son RGBA; `copyTo` no tiene target gray |
