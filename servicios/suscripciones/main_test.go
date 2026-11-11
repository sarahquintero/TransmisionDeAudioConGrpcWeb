package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPagarYValidarTokenUnSoloUso(t *testing.T) {
	store := newSubscriptionStore()

	// 1. Pagar genera un token UUID válido
	payReq := httptest.NewRequest(http.MethodPost, "/pagar", bytes.NewBufferString(`{"plan":"Premium"}`))
	payRec := httptest.NewRecorder()
	store.handlePagar(payRec, payReq)

	if payRec.Code != http.StatusOK {
		t.Fatalf("esperado 200 en /pagar, obtenido %d", payRec.Code)
	}
	var payBody struct {
		Token string `json:"token"`
		Plan  string `json:"plan"`
	}
	if err := json.Unmarshal(payRec.Body.Bytes(), &payBody); err != nil {
		t.Fatalf("JSON inválido en /pagar: %v", err)
	}
	if payBody.Token == "" || payBody.Plan != "Premium" {
		t.Fatalf("respuesta inesperada en /pagar: %+v", payBody)
	}

	// 2. Primera validación: éxito (200) y marca como usado
	body := `{"token":"` + payBody.Token + `"}`
	val1Req := httptest.NewRequest(http.MethodPost, "/validar-token", bytes.NewBufferString(body))
	val1Rec := httptest.NewRecorder()
	store.handleValidarToken(val1Rec, val1Req)

	if val1Rec.Code != http.StatusOK {
		t.Fatalf("esperado 200 en primera validación, obtenido %d: %s", val1Rec.Code, val1Rec.Body.String())
	}

	// 3. Reutilizar el mismo token: debe fallar con 409 Conflict
	val2Req := httptest.NewRequest(http.MethodPost, "/validar-token", bytes.NewBufferString(body))
	val2Rec := httptest.NewRecorder()
	store.handleValidarToken(val2Rec, val2Req)

	if val2Rec.Code != http.StatusConflict {
		t.Fatalf("esperado 409 al reutilizar token, obtenido %d: %s", val2Rec.Code, val2Rec.Body.String())
	}

	// 4. Token inexistente: 404 Not Found
	badReq := httptest.NewRequest(http.MethodPost, "/validar-token", bytes.NewBufferString(`{"token":"no-existe"}`))
	badRec := httptest.NewRecorder()
	store.handleValidarToken(badRec, badReq)

	if badRec.Code != http.StatusNotFound {
		t.Fatalf("esperado 404 para token inexistente, obtenido %d", badRec.Code)
	}
}
