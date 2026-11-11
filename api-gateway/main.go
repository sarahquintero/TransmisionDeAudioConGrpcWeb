// API Gateway: punto de entrada único del sistema.
//
//	Navegador ──HTTP/WS/gRPC-Web──▶ API Gateway ──▶ (Consul resuelve) ──▶ microservicios
//	                                     │
//	                                     └─ /grpc/* ──▶ Envoy ──gRPC/HTTP2──▶ Servidor de Streaming
//
// Las direcciones de los servicios no están fijas: en cada petición (con una
// caché corta) se consulta a Consul por las instancias saludables.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"transmision/pkg/consul"
	"transmision/pkg/servicekit"
)

func main() {
	cfg := servicekit.LoadConfig("api-gateway", 8000)
	streamingProxy := servicekit.Env("STREAMING_PROXY_SERVICE", "envoy")
	cacheTTL, err := time.ParseDuration(servicekit.Env("DISCOVERY_CACHE_TTL", "5s"))
	if err != nil {
		cacheTTL = 5 * time.Second
	}
	origins := strings.Split(servicekit.Env("CORS_ALLOWED_ORIGINS", "*"), ",")

	consulClient := consul.NewClient(cfg.ConsulAddr)
	resolver := NewResolver(consulClient, cacheTTL)
	gateway := NewGateway(resolver)
	routes := buildRoutes(streamingProxy)

	mux := servicekit.NewMux(cfg) // GET /health

	// Endpoints propios del gateway (diagnóstico).
	mux.HandleFunc("GET /gateway/routes", func(w http.ResponseWriter, _ *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, routes)
	})
	mux.HandleFunc("GET /gateway/services", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		services, err := consulClient.Services(ctx)
		if err != nil {
			servicekit.WriteJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
		servicekit.WriteJSON(w, http.StatusOK, services)
	})

	// Rutas dinámicas hacia los microservicios.
	for _, rt := range routes {
		h := gateway.Handler(rt)
		mux.Handle(rt.Prefix, h)     // coincidencia exacta: /api/audios
		mux.Handle(rt.Prefix+"/", h) // subárbol:            /api/audios/...
		log.Printf("[gateway] ruta %-20s -> consul:%s", rt.Prefix+"/*", rt.Service)
	}

	handler := withRecover(withRequestID(withLogging(withCORS(origins, mux))))

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// Sin WriteTimeout: los streams de audio y los WebSockets son de larga duración.
	}

	ctx, stop := servicekit.ShutdownContext()
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[gateway] escuchando en %s (Consul: %s, proxy streaming: %s)", srv.Addr, cfg.ConsulAddr, streamingProxy)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	// El gateway también se registra en Consul para tener visibilidad completa.
	deregister, err := servicekit.Register(ctx, cfg, servicekit.HTTPCheck(cfg, cfg.Port))
	if err != nil {
		log.Printf("[gateway] ADVERTENCIA: no se pudo registrar en Consul: %v", err)
	}
	defer deregister()

	select {
	case <-ctx.Done():
		log.Printf("[gateway] señal de apagado recibida")
	case err := <-errCh:
		log.Printf("[gateway] error del servidor: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
