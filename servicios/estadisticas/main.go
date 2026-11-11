// Servidor de Estadísticas (Fase 5).
//
// Registra eventos de reproducción aislados por usuario (client_id) y entrega
// su historial acumulado:
//   - POST /reproducir               -> incrementa el contador de (client_id, titulo)
//   - GET  /historial?client_id=...   -> devuelve el historial exclusivo de ese client_id
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"transmision/pkg/servicekit"
)

type playStat struct {
	Titulo             string    `json:"titulo"`
	Reproducciones     int       `json:"reproducciones"`
	Texto              string    `json:"texto"`
	UltimaReproduccion time.Time `json:"ultima_reproduccion"`
}

type statsStore struct {
	mu    sync.RWMutex
	plays map[string]map[string]*playStat // client_id -> titulo -> *playStat
}

func newStatsStore() *statsStore {
	return &statsStore{
		plays: make(map[string]map[string]*playStat),
	}
}

func formatStatText(titulo string, count int) string {
	unidad := "veces"
	if count == 1 {
		unidad = "vez"
	}
	return fmt.Sprintf("%s - Reproducido %d %s", titulo, count, unidad)
}

func normalizeClientID(raw string) string {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "anonimo"
	}
	return id
}

func (s *statsStore) handleReproducir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string `json:"client_id"`
		AudioID  string `json:"audio_id"`
		Titulo   string `json:"titulo"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido"})
		return
	}

	clientID := normalizeClientID(req.ClientID)
	if clientID == "anonimo" {
		if hdr := strings.TrimSpace(r.Header.Get("X-Client-ID")); hdr != "" {
			clientID = hdr
		}
	}

	titulo := strings.TrimSpace(req.Titulo)
	if titulo == "" {
		titulo = strings.TrimSpace(req.AudioID)
	}
	if titulo == "" {
		servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "se requiere 'titulo' o 'audio_id'"})
		return
	}

	s.mu.Lock()
	userPlays, exists := s.plays[clientID]
	if !exists {
		userPlays = make(map[string]*playStat)
		s.plays[clientID] = userPlays
	}
	st, ok := userPlays[titulo]
	if !ok {
		st = &playStat{Titulo: titulo}
		userPlays[titulo] = st
	}
	st.Reproducciones++
	st.UltimaReproduccion = time.Now().UTC()
	st.Texto = formatStatText(st.Titulo, st.Reproducciones)
	snapshot := *st
	s.mu.Unlock()

	log.Printf("[estadisticas] client=%q reproducción: %s", clientID, snapshot.Texto)
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"client_id":      clientID,
		"titulo":         snapshot.Titulo,
		"reproducciones": snapshot.Reproducciones,
		"texto":          snapshot.Texto,
	})
}

func (s *statsStore) handleHistorial(w http.ResponseWriter, r *http.Request) {
	clientID := normalizeClientID(r.URL.Query().Get("client_id"))
	if clientID == "anonimo" {
		if hdr := strings.TrimSpace(r.Header.Get("X-Client-ID")); hdr != "" {
			clientID = hdr
		}
	}

	s.mu.RLock()
	userPlays := s.plays[clientID]
	items := make([]playStat, 0, len(userPlays))
	total := 0
	for _, st := range userPlays {
		items = append(items, *st)
		total += st.Reproducciones
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].Reproducciones != items[j].Reproducciones {
			return items[i].Reproducciones > items[j].Reproducciones
		}
		return items[i].Titulo < items[j].Titulo
	})

	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"client_id":            clientID,
		"total_reproducciones": total,
		"historial":            items,
	})
}

func main() {
	cfg := servicekit.LoadConfig("estadisticas", 8085)
	store := newStatsStore()

	mux := servicekit.NewMux(cfg)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{
			"service": cfg.Name,
			"message": "Servidor de Estadísticas activo",
		})
	})
	mux.HandleFunc("POST /reproducir", store.handleReproducir)
	mux.HandleFunc("GET /historial", store.handleHistorial)

	servicekit.RunHTTP(cfg, mux)
}
