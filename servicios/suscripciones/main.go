// Servidor de Suscripciones (Fase 3).
//
// Gestiona pagos simulados y tokens de suscripción de un solo uso:
//   - POST /pagar          -> simula el pago y devuelve un token UUID
//   - POST /validar-token  -> valida el token; si ya fue usado devuelve error,
//     si es válido lo marca como usado y devuelve éxito
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"transmision/pkg/servicekit"
)

type tokenRecord struct {
	Token     string     `json:"token"`
	Plan      string     `json:"plan"`
	Used      bool       `json:"usado"`
	CreatedAt time.Time  `json:"creado_en"`
	UsedAt    *time.Time `json:"usado_en,omitempty"`
}

type subscriptionStore struct {
	mu     sync.Mutex
	tokens map[string]*tokenRecord
}

func newSubscriptionStore() *subscriptionStore {
	return &subscriptionStore{
		tokens: make(map[string]*tokenRecord),
	}
}

func (s *subscriptionStore) handlePagar(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Plan string `json:"plan"`
	}
	// El cuerpo es opcional: si viene vacío usamos el plan por defecto.
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	}
	plan := strings.TrimSpace(req.Plan)
	if plan == "" {
		plan = "Mensual"
	}

	tok := uuid.NewString()
	rec := &tokenRecord{
		Token:     tok,
		Plan:      plan,
		Used:      false,
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.tokens[tok] = rec
	s.mu.Unlock()

	log.Printf("[suscripciones] pago simulado aprobado plan=%q token=%s", plan, tok)
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"token":   tok,
		"plan":    plan,
		"usado":   false,
		"mensaje": "Pago exitoso. Token de suscripción generado.",
	})
}

func (s *subscriptionStore) handleValidarToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		servicekit.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"valido": false,
			"error":  "JSON inválido",
		})
		return
	}

	tok := strings.TrimSpace(req.Token)
	if tok == "" {
		servicekit.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"ok":     false,
			"valido": false,
			"error":  "El campo 'token' es obligatorio",
		})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rec, exists := s.tokens[tok]
	if !exists {
		log.Printf("[suscripciones] intento de validar token inexistente: %s", tok)
		servicekit.WriteJSON(w, http.StatusNotFound, map[string]any{
			"ok":     false,
			"valido": false,
			"error":  "Token no encontrado o inválido",
		})
		return
	}
	if rec.Used {
		log.Printf("[suscripciones] token ya utilizado rechazado: %s", tok)
		servicekit.WriteJSON(w, http.StatusConflict, map[string]any{
			"ok":     false,
			"valido": false,
			"error":  "El token ya fue usado anteriormente",
		})
		return
	}

	now := time.Now().UTC()
	rec.Used = true
	rec.UsedAt = &now

	log.Printf("[suscripciones] token validado y marcado como usado: %s (plan=%q)", tok, rec.Plan)
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"valido":  true,
		"token":   rec.Token,
		"plan":    rec.Plan,
		"mensaje": "Token válido. Suscripción activada (token consumido).",
	})
}

func main() {
	cfg := servicekit.LoadConfig("suscripciones", 8083)
	store := newSubscriptionStore()

	mux := servicekit.NewMux(cfg)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{
			"service": cfg.Name,
			"message": "Servidor de Suscripciones activo",
		})
	})
	mux.HandleFunc("POST /pagar", store.handlePagar)
	mux.HandleFunc("POST /validar-token", store.handleValidarToken)

	servicekit.RunHTTP(cfg, mux)
}
