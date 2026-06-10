package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/pion/webrtc/v4"

	"face-capture-poc/backend/internal/config"
	"face-capture-poc/backend/internal/session"
)

// WebRTCEngine multiplexa TODO el tráfico ICE/DTLS/SCTP en UN puerto UDP
// (webrtc.NewICEUDPMux). Es la única topología viable en Fly.io: su proxy UDP
// no reescribe puertos destino ni soporta rangos, y exige bindear a
// fly-global-services. El rango 50000-50100 del PRD §2 queda reemplazado.
type WebRTCEngine struct {
	api *webrtc.API
	cfg config.Config
	log *slog.Logger
}

func NewWebRTCEngine(cfg config.Config, log *slog.Logger) (*WebRTCEngine, error) {
	if log == nil {
		log = slog.Default()
	}

	udpConn, bindAddr, err := bindUDP(cfg)
	if err != nil {
		return nil, err
	}
	log.Info("webrtc udp mux listening", "addr", bindAddr, "port", cfg.UDPPort)

	se := webrtc.SettingEngine{}
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, udpConn))

	// En Fly la máquina solo tiene IPs privadas: anunciar la IPv4 dedicada
	// como candidato host (la reemplaza, no la agrega — tipo Host, no Srflx).
	if ip := publicIPv4(cfg, log); ip != "" {
		se.SetNAT1To1IPs([]string{ip}, webrtc.ICECandidateTypeHost)
		log.Info("webrtc advertising public ip", "ip", ip)
	}

	return &WebRTCEngine{
		api: webrtc.NewAPI(webrtc.WithSettingEngine(se)),
		cfg: cfg,
		log: log,
	}, nil
}

// bindUDP: en Fly (FLY_APP_NAME presente) el socket DEBE bindear a
// fly-global-services:<puerto> — 0.0.0.0 nunca recibe el UDP proxiado y el
// reply debe salir por el mismo socket. En local, 0.0.0.0.
func bindUDP(cfg config.Config) (net.PacketConn, string, error) {
	host := "0.0.0.0"
	if cfg.FlyAppName != "" {
		host = "fly-global-services"
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, cfg.UDPPort))
	if err != nil {
		return nil, "", fmt.Errorf("webrtc: resolve %s:%d: %w", host, cfg.UDPPort, err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, "", fmt.Errorf("webrtc: bind udp %v: %w", addr, err)
	}
	return conn, host, nil
}

// publicIPv4: PUBLIC_IP explícita, o fallback auto-configurante en Fly
// resolviendo el A record de <app>.fly.dev (la IPv4 dedicada).
func publicIPv4(cfg config.Config, log *slog.Logger) string {
	if cfg.PublicIP != "" {
		return cfg.PublicIP
	}
	if cfg.FlyAppName == "" {
		return "" // dev local: pion anuncia las IPs LAN reales
	}
	ips, err := net.LookupIP(cfg.FlyAppName + ".fly.dev")
	if err != nil {
		log.Warn("no se pudo resolver la IP pública; setear PUBLIC_IP", "err", err)
		return ""
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
	}
	log.Warn("sin A record IPv4: UDP en Fly requiere IPv4 dedicada (fly ips allocate-v4)")
	return ""
}

// answerTimeout acota el gathering del lado servidor. Sin STUN configurado y
// con NAT1To1, completa en milisegundos; el timeout es solo red de seguridad.
const answerTimeout = 3 * time.Second

// CreateAnswer implementa la señalización non-trickle de un solo POST: aplica
// la offer, cablea el DataChannel al ingest y devuelve la answer con todos los
// candidatos ya incluidos.
func (e *WebRTCEngine) CreateAnswer(ctx context.Context, s *session.Session, offer webrtc.SessionDescription) (*webrtc.SessionDescription, error) {
	pc, err := e.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("webrtc: peer connection: %w", err)
	}

	log := e.log.With("sessionId", s.ID)

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnOpen(func() {
			s.SetTransport("webrtc")
			// Verificación de campo: la semántica unreliable viaja en el DCEP
			// del cliente ({ordered:false, maxRetransmits:0}).
			maxRetransmits := -1
			if v := dc.MaxRetransmits(); v != nil {
				maxRetransmits = int(*v)
			}
			log.Info("datachannel open",
				"label", dc.Label(), "ordered", dc.Ordered(), "maxRetransmits", maxRetransmits)
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if msg.IsString {
				HandleControl(s, msg.Data, e.log)
				return
			}
			HandleDatagram(s, msg.Data)
		})
		dc.OnClose(func() {
			s.Reasm.NoteTransportClosed()
		})
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		log.Info("webrtc state", "state", st.String())
		switch st {
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected:
			s.Reasm.NoteTransportClosed()
			_ = pc.Close()
		default:
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("webrtc: set remote: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("webrtc: create answer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("webrtc: set local: %w", err)
	}

	select {
	case <-gathered:
	case <-time.After(answerTimeout):
		log.Warn("ice gathering no completó; respondiendo con candidatos parciales")
	case <-ctx.Done():
		_ = pc.Close()
		return nil, ctx.Err()
	}

	return pc.LocalDescription(), nil
}
