// Servidor de Reacciones con WebSockets (Fase 3).
//
// Expone GET /ws:
//   - Los clientes se conectan y se suscriben a un audio_id (por query ?audio_id=...
//     o enviando un mensaje JSON {"type":"subscribe","audio_id":"..."}).
//   - Cuando un cliente envía un evento ("play", "pause", etc.), el servidor
//     hace broadcast a todos los demás clientes suscritos al mismo audio_id.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"transmision/pkg/servicekit"
)

type wsMessage struct {
	Type      string `json:"type"`
	AudioID   string `json:"audio_id"`
	Emoji     string `json:"emoji,omitempty"`
	Icon      string `json:"icon,omitempty"`
	Sender    string `json:"sender,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type clientConn struct {
	conn    *websocket.Conn
	audioID string
	send    chan []byte
}

type reactionHub struct {
	mu      sync.RWMutex
	clients map[*clientConn]struct{}
}

func newReactionHub() *reactionHub {
	return &reactionHub{
		clients: make(map[*clientConn]struct{}),
	}
}

func (h *reactionHub) add(c *clientConn) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *reactionHub) remove(c *clientConn) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
	}
	h.mu.Unlock()
}

func (h *reactionHub) setSubscription(c *clientConn, audioID string) {
	h.mu.Lock()
	c.audioID = audioID
	h.mu.Unlock()
}

// broadcastToOthers reenvía payload exclusivamente a los demás clientes
// suscritos al mismo audioID (aislamiento estricto por canción).
func (h *reactionHub) broadcastToOthers(sender *clientConn, audioID string, payload []byte) int {
	if audioID == "" {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()

	delivered := 0
	for c := range h.clients {
		if c == sender || c.audioID != audioID {
			continue
		}
		select {
		case c.send <- payload:
			delivered++
		default:
			// Buffer lleno: descartamos el mensaje para no bloquear al hub.
		}
	}
	return delivered
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(_ *http.Request) bool { return true },
}

func (h *reactionHub) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[reacciones] error en upgrade WebSocket: %v", err)
		return
	}

	initialAudioID := strings.TrimSpace(r.URL.Query().Get("audio_id"))
	client := &clientConn{
		conn:    conn,
		audioID: initialAudioID,
		send:    make(chan []byte, 32),
	}
	h.add(client)
	log.Printf("[reacciones] cliente conectado (%s) audio_id=%q", r.RemoteAddr, initialAudioID)

	// Bomba de escritura
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer func() {
			ticker.Stop()
			_ = conn.Close()
		}()
		for {
			select {
			case msg, ok := <-client.send:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Bucle de lectura
	defer func() {
		h.remove(client)
		log.Printf("[reacciones] cliente desconectado (%s)", r.RemoteAddr)
	}()

	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

		var msg wsMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			log.Printf("[reacciones] mensaje WS no JSON ignorado: %v", err)
			continue
		}

		msg.Type = strings.ToLower(strings.TrimSpace(msg.Type))
		msg.AudioID = strings.TrimSpace(msg.AudioID)
		if msg.Timestamp == "" {
			msg.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}

		if msg.Type == "subscribe" {
			h.setSubscription(client, msg.AudioID)
			log.Printf("[reacciones] cliente %s suscrito a audio_id=%q", r.RemoteAddr, msg.AudioID)
			ack, _ := json.Marshal(wsMessage{
				Type:      "subscribed",
				AudioID:   msg.AudioID,
				Timestamp: msg.Timestamp,
			})
			select {
			case client.send <- ack:
			default:
			}
			continue
		}

		targetAudio := msg.AudioID
		if targetAudio == "" {
			targetAudio = client.audioID
			msg.AudioID = targetAudio
		}

		out, _ := json.Marshal(msg)
		n := h.broadcastToOthers(client, targetAudio, out)
		log.Printf("[reacciones] broadcast type=%q audio_id=%q -> %d cliente(s)", msg.Type, targetAudio, n)
	}
}

func main() {
	cfg := servicekit.LoadConfig("reacciones", 8082)
	hub := newReactionHub()

	mux := servicekit.NewMux(cfg)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{
			"service": cfg.Name,
			"message": "Servidor de Reacciones (WebSockets) activo",
		})
	})
	mux.HandleFunc("GET /ws", hub.handleWS)

	servicekit.RunHTTP(cfg, mux)
}
