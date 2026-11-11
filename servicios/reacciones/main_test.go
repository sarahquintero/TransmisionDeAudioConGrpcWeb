package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func subscribeAndWaitAck(t *testing.T, c *websocket.Conn, audioID string) {
	t.Helper()
	if err := c.WriteJSON(wsMessage{Type: "subscribe", AudioID: audioID}); err != nil {
		t.Fatalf("error enviando subscribe (%s): %v", audioID, err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var ack wsMessage
	if err := c.ReadJSON(&ack); err != nil {
		t.Fatalf("no se recibió ack de subscribe (%s): %v", audioID, err)
	}
	if ack.Type != "subscribed" || ack.AudioID != audioID {
		t.Fatalf("ack inesperado: %+v", ack)
	}
}

func TestWebSocketBroadcastAOtrosSuscritos(t *testing.T) {
	hub := newReactionHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.handleWS))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "?audio_id=cancion1"

	c1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("error conectando c1: %v", err)
	}
	defer c1.Close()

	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("error conectando c2: %v", err)
	}
	defer c2.Close()

	cOtherRoom, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?audio_id=podcast1", nil)
	if err != nil {
		t.Fatalf("error conectando cOtherRoom: %v", err)
	}
	defer cOtherRoom.Close()

	subscribeAndWaitAck(t, c1, "cancion1")
	subscribeAndWaitAck(t, c2, "cancion1")
	subscribeAndWaitAck(t, cOtherRoom, "podcast1")

	if err := c1.WriteJSON(wsMessage{Type: "reaction", AudioID: "cancion1", Emoji: "❤️", Sender: "c1"}); err != nil {
		t.Fatalf("error enviando reacción: %v", err)
	}

	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got wsMessage
	if err := c2.ReadJSON(&got); err != nil {
		t.Fatalf("c2 no recibió broadcast: %v", err)
	}
	if got.Type != "reaction" || got.AudioID != "cancion1" || got.Emoji != "❤️" {
		t.Fatalf("mensaje inesperado en c2: %+v", got)
	}

	// Ni c1 (emisor) ni cOtherRoom (otra sala) deben recibir el mensaje.
	_ = c1.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if err := c1.ReadJSON(&got); err == nil {
		t.Fatalf("el emisor no debe recibir su propio broadcast: %+v", got)
	}
	_ = cOtherRoom.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if err := cOtherRoom.ReadJSON(&got); err == nil {
		t.Fatalf("otra sala no debe recibir el broadcast: %+v", got)
	}
}
