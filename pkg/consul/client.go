// Package consul implementa un cliente mínimo para la API HTTP de Consul.
//
// Se usa la API REST directamente (en lugar del SDK oficial) para mantener
// los microservicios ligeros: solo necesitamos registrar, desregistrar y
// consultar instancias saludables de un servicio.
package consul

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Check describe un health check que Consul ejecutará contra la instancia.
// Debe definirse exactamente uno de HTTP o GRPC.
type Check struct {
	CheckID                        string `json:"CheckID,omitempty"`
	Name                           string `json:"Name,omitempty"`
	HTTP                           string `json:"HTTP,omitempty"`
	GRPC                           string `json:"GRPC,omitempty"`
	GRPCUseTLS                     bool   `json:"GRPCUseTLS,omitempty"`
	Interval                       string `json:"Interval,omitempty"`
	Timeout                        string `json:"Timeout,omitempty"`
	DeregisterCriticalServiceAfter string `json:"DeregisterCriticalServiceAfter,omitempty"`
}

// Registration es el cuerpo de PUT /v1/agent/service/register.
type Registration struct {
	ID      string            `json:"ID"`
	Name    string            `json:"Name"`
	Address string            `json:"Address"`
	Port    int               `json:"Port"`
	Tags    []string          `json:"Tags,omitempty"`
	Meta    map[string]string `json:"Meta,omitempty"`
	Checks  []Check           `json:"Checks,omitempty"`
}

// Instance es una instancia saludable de un servicio resuelta desde Consul.
type Instance struct {
	ID      string
	Service string
	Address string
	Port    int
	Tags    []string
	Meta    map[string]string
}

// HostPort devuelve "address:port" listo para construir una URL.
func (i Instance) HostPort() string {
	return fmt.Sprintf("%s:%d", i.Address, i.Port)
}

// Client habla con un agente de Consul.
type Client struct {
	base string
	http *http.Client
}

// NewClient crea un cliente. addr acepta "consul:8500" o "http://consul:8500".
func NewClient(addr string) *Client {
	if addr == "" {
		addr = "http://localhost:8500"
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return &Client{
		base: strings.TrimRight(addr, "/"),
		http: &http.Client{Timeout: 5 * time.Second},
	}
}

// Register registra (o actualiza) una instancia en el agente local.
func (c *Client) Register(ctx context.Context, reg Registration) error {
	return c.do(ctx, http.MethodPut, "/v1/agent/service/register", reg, nil)
}

// RegisterWithRetry reintenta el registro con backoff hasta que Consul esté
// disponible o se agote el contexto. Útil al arrancar dentro de Docker,
// donde Consul puede tardar unos segundos en aceptar peticiones.
func (c *Client) RegisterWithRetry(ctx context.Context, reg Registration) error {
	backoff := 500 * time.Millisecond
	for {
		err := c.Register(ctx, reg)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("registro en consul cancelado: %w (último error: %v)", ctx.Err(), err)
		case <-time.After(backoff):
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
}

// Deregister elimina una instancia del catálogo.
func (c *Client) Deregister(ctx context.Context, serviceID string) error {
	return c.do(ctx, http.MethodPut, "/v1/agent/service/deregister/"+url.PathEscape(serviceID), nil, nil)
}

// HealthyInstances devuelve solo las instancias cuyos checks están en "passing".
func (c *Client) HealthyInstances(ctx context.Context, service string) ([]Instance, error) {
	var entries []struct {
		Node struct {
			Address string `json:"Address"`
		} `json:"Node"`
		Service struct {
			ID      string            `json:"ID"`
			Service string            `json:"Service"`
			Address string            `json:"Address"`
			Port    int               `json:"Port"`
			Tags    []string          `json:"Tags"`
			Meta    map[string]string `json:"Meta"`
		} `json:"Service"`
	}
	path := "/v1/health/service/" + url.PathEscape(service) + "?passing=true"
	if err := c.do(ctx, http.MethodGet, path, nil, &entries); err != nil {
		return nil, err
	}

	out := make([]Instance, 0, len(entries))
	for _, e := range entries {
		addr := e.Service.Address
		if addr == "" { // Consul usa la IP del nodo si el servicio no declara dirección.
			addr = e.Node.Address
		}
		out = append(out, Instance{
			ID:      e.Service.ID,
			Service: e.Service.Service,
			Address: addr,
			Port:    e.Service.Port,
			Tags:    e.Service.Tags,
			Meta:    e.Service.Meta,
		})
	}
	return out, nil
}

// ServiceHealth resume el estado de un servicio para paneles/diagnóstico.
type ServiceHealth struct {
	Name    string `json:"name"`
	Total   int    `json:"total"`
	Passing int    `json:"passing"`
}

// Services lista los nombres de servicios del catálogo con su conteo de
// instancias totales y saludables.
func (c *Client) Services(ctx context.Context) ([]ServiceHealth, error) {
	var catalog map[string][]string
	if err := c.do(ctx, http.MethodGet, "/v1/catalog/services", nil, &catalog); err != nil {
		return nil, err
	}

	out := make([]ServiceHealth, 0, len(catalog))
	for name := range catalog {
		var all []json.RawMessage
		if err := c.do(ctx, http.MethodGet, "/v1/health/service/"+url.PathEscape(name), nil, &all); err != nil {
			return nil, err
		}
		healthy, err := c.HealthyInstances(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, ServiceHealth{Name: name, Total: len(all), Passing: len(healthy)})
	}
	return out, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("consul %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
