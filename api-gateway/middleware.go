package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// statusRecorder captura el código de estado para el log. Implementa Unwrap
// para que http.ResponseController (usado por httputil.ReverseProxy) pueda
// acceder a Flush/Hijack del writer original: necesario para gRPC-Web y
// WebSockets.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withLogging registra método, ruta, estado y duración de cada petición.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/health" {
			return // evita ruido de los healthchecks.
		}
		log.Printf("[gateway] %s %s -> %d (%s) rid=%s",
			r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), r.Header.Get("X-Request-ID"))
	})
}

// withRequestID propaga (o genera) un X-Request-ID para trazabilidad.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// withRecover evita que un panic tumbe el gateway.
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // señal interna del ReverseProxy; se respeta.
				}
				log.Printf("[gateway] panic: %v\n%s", v, debug.Stack())
				http.Error(w, "error interno", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Cabeceras que el navegador necesita enviar/leer para gRPC-Web.
const (
	corsAllowHeaders = "Content-Type, Authorization, X-Request-ID, X-Grpc-Web, X-User-Agent, " +
		"Grpc-Timeout, Keep-Alive, User-Agent, Cache-Control, Content-Transfer-Encoding, " +
		"X-Accept-Content-Transfer-Encoding, X-Accept-Response-Streaming"
	corsExposeHeaders = "Grpc-Status, Grpc-Message, Grpc-Status-Details-Bin, X-Request-ID, " +
		"X-Upstream-Service, X-Upstream-Instance"
)

// withCORS aplica la política CORS de forma centralizada.
// allowed = ["*"] permite cualquier origen.
func withCORS(allowed []string, next http.Handler) http.Handler {
	allowAll := false
	set := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		o = strings.TrimSpace(o)
		if o == "*" {
			allowAll = true
		}
		set[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowAll || set[origin]) {
			h := w.Header()
			if allowAll {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			h.Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
