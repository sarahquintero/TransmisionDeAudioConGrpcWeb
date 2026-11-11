// Package servicekit agrupa el código común de arranque de los microservicios:
// configuración por variables de entorno, endpoint /health, registro en Consul
// y apagado ordenado (graceful shutdown) con desregistro.
package servicekit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"transmision/pkg/consul"
)

// Config se construye a partir de variables de entorno.
type Config struct {
	Name       string   // SERVICE_NAME: nombre lógico en Consul.
	Host       string   // SERVICE_HOST: host con el que otros contenedores alcanzan esta instancia.
	Port       int      // SERVICE_PORT: puerto HTTP (o gRPC en el servicio de streaming).
	ConsulAddr string   // CONSUL_HTTP_ADDR: dirección del agente Consul.
	Tags       []string // SERVICE_TAGS: etiquetas separadas por coma.
}

// InstanceID identifica de forma única la instancia (permite escalar réplicas).
func (c Config) InstanceID() string {
	h, _ := os.Hostname()
	if h == "" {
		h = c.Host
	}
	return fmt.Sprintf("%s-%s-%d", c.Name, h, c.Port)
}

// LoadConfig lee la configuración aplicando valores por defecto.
func LoadConfig(defaultName string, defaultPort int) Config {
	// Si no se fija SERVICE_HOST se anuncia la IP del contenedor: así cada
	// réplica (docker compose up --scale X=N) se registra con su propia dirección.
	host := Env("SERVICE_HOST", "")
	if host == "" {
		host = localIPv4()
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	var tags []string
	if raw := Env("SERVICE_TAGS", ""); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}
	return Config{
		Name:       Env("SERVICE_NAME", defaultName),
		Host:       host,
		Port:       EnvInt("SERVICE_PORT", defaultPort),
		ConsulAddr: Env("CONSUL_HTTP_ADDR", "consul:8500"),
		Tags:       tags,
	}
}

// localIPv4 devuelve la primera IPv4 no loopback de la máquina/contenedor.
func localIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// Env devuelve la variable de entorno o un valor por defecto.
func Env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// EnvInt devuelve la variable de entorno como entero o un valor por defecto.
func EnvInt(key string, def int) int {
	if v, err := strconv.Atoi(Env(key, "")); err == nil {
		return v
	}
	return def
}

var startedAt = time.Now()

// HealthHandler responde 200 con un JSON describiendo la instancia.
func HealthHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{
			"status":   "UP",
			"service":  cfg.Name,
			"instance": cfg.InstanceID(),
			"uptime":   time.Since(startedAt).Round(time.Second).String(),
			"time":     time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// WriteJSON serializa v como JSON con el código de estado indicado.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// HTTPCheck construye el health check HTTP estándar contra /health.
func HTTPCheck(cfg Config, port int) consul.Check {
	return consul.Check{
		CheckID:                        cfg.InstanceID() + "-http",
		Name:                           cfg.Name + " HTTP /health",
		HTTP:                           fmt.Sprintf("http://%s:%d/health", cfg.Host, port),
		Interval:                       "10s",
		Timeout:                        "2s",
		DeregisterCriticalServiceAfter: "1m",
	}
}

// Register registra la instancia en Consul (con reintentos) y devuelve una
// función que la desregistra.
func Register(ctx context.Context, cfg Config, checks ...consul.Check) (func(), error) {
	client := consul.NewClient(cfg.ConsulAddr)
	reg := consul.Registration{
		ID:      cfg.InstanceID(),
		Name:    cfg.Name,
		Address: cfg.Host,
		Port:    cfg.Port,
		Tags:    cfg.Tags,
		Meta:    map[string]string{"version": Env("SERVICE_VERSION", "0.1.0")},
		Checks:  checks,
	}

	regCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := client.RegisterWithRetry(regCtx, reg); err != nil {
		return func() {}, err
	}
	log.Printf("[%s] registrado en Consul como %q (%s:%d)", cfg.Name, reg.ID, reg.Address, reg.Port)

	return func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer dcancel()
		if err := client.Deregister(dctx, reg.ID); err != nil {
			log.Printf("[%s] error al desregistrar de Consul: %v", cfg.Name, err)
			return
		}
		log.Printf("[%s] desregistrado de Consul", cfg.Name)
	}, nil
}

// ShutdownContext devuelve un contexto que se cancela con SIGINT/SIGTERM.
func ShutdownContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// RunHTTP levanta un servidor HTTP con el handler dado, lo registra en Consul
// con un check HTTP a /health y bloquea hasta recibir una señal de apagado.
// El handler debe exponer /health (ver NewMux).
func RunHTTP(cfg Config, handler http.Handler) {
	ctx, stop := ShutdownContext()
	defer stop()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[%s] escuchando HTTP en %s", cfg.Name, srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	deregister, err := Register(ctx, cfg, HTTPCheck(cfg, cfg.Port))
	if err != nil {
		log.Printf("[%s] ADVERTENCIA: no se pudo registrar en Consul: %v", cfg.Name, err)
	}
	defer deregister()

	select {
	case <-ctx.Done():
		log.Printf("[%s] señal de apagado recibida", cfg.Name)
	case err := <-errCh:
		log.Printf("[%s] error del servidor HTTP: %v", cfg.Name, err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// NewMux crea un ServeMux con /health ya montado.
func NewMux(cfg Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", HealthHandler(cfg))
	return mux
}
