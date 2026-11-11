package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReproducirYHistorialAisladoPorUsuario(t *testing.T) {
	store := newStatsStore()

	// Usuario A reproduce cancion1 (4 veces) y podcast1 (1 vez)
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/reproducir",
			bytes.NewBufferString(`{"client_id":"user-A","titulo":"cancion1"}`))
		rec := httptest.NewRecorder()
		store.handleReproducir(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("esperado 200 en /reproducir, obtenido %d", rec.Code)
		}
	}
	store.handleReproducir(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/reproducir", bytes.NewBufferString(`{"client_id":"user-A","titulo":"podcast1"}`)))

	// Usuario B reproduce solo ruido1 (2 veces)
	for i := 0; i < 2; i++ {
		store.handleReproducir(httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/reproducir", bytes.NewBufferString(`{"client_id":"user-B","titulo":"ruido1"}`)))
	}

	// Historial de user-A solo contiene sus reproducciones
	histARec := httptest.NewRecorder()
	store.handleHistorial(histARec, httptest.NewRequest(http.MethodGet, "/historial?client_id=user-A", nil))
	var bodyA struct {
		ClientID  string     `json:"client_id"`
		Total     int        `json:"total_reproducciones"`
		Historial []playStat `json:"historial"`
	}
	if err := json.Unmarshal(histARec.Body.Bytes(), &bodyA); err != nil {
		t.Fatalf("JSON inválido en /historial user-A: %v", err)
	}
	if bodyA.ClientID != "user-A" || bodyA.Total != 5 || len(bodyA.Historial) != 2 {
		t.Fatalf("totales inesperados user-A: %+v", bodyA)
	}
	if bodyA.Historial[0].Texto != "cancion1 - Reproducido 4 veces" {
		t.Fatalf("formato esperado 'cancion1 - Reproducido 4 veces', obtenido %q", bodyA.Historial[0].Texto)
	}
	if bodyA.Historial[1].Texto != "podcast1 - Reproducido 1 vez" {
		t.Fatalf("formato esperado 'podcast1 - Reproducido 1 vez', obtenido %q", bodyA.Historial[1].Texto)
	}

	// Historial de user-B solo contiene ruido1
	histBRec := httptest.NewRecorder()
	store.handleHistorial(histBRec, httptest.NewRequest(http.MethodGet, "/historial?client_id=user-B", nil))
	var bodyB struct {
		Total     int        `json:"total_reproducciones"`
		Historial []playStat `json:"historial"`
	}
	_ = json.Unmarshal(histBRec.Body.Bytes(), &bodyB)
	if bodyB.Total != 2 || len(bodyB.Historial) != 1 || bodyB.Historial[0].Titulo != "ruido1" {
		t.Fatalf("historial user-B no aislado: %+v", bodyB)
	}
}
