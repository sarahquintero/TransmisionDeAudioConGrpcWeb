package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"transmision/pkg/consul"
	"transmision/pkg/servicekit"
)

type upstreamKey struct{}

// upstream es el destino resuelto para una petición concreta.
type upstream struct {
	route    Route
	instance consul.Instance
	target   *url.URL
}

// Gateway enruta peticiones hacia instancias resueltas dinámicamente en Consul.
type Gateway struct {
	resolver *Resolver
	proxy    *httputil.ReverseProxy
}

// NewGateway construye el reverse proxy compartido por todas las rutas.
//
// Notas de diseño:
//   - FlushInterval = -1: cada fragmento recibido se envía inmediatamente al
//     cliente. Imprescindible para gRPC-Web server-streaming (audio por chunks).
//   - httputil.ReverseProxy soporta "Connection: Upgrade", por lo que el
//     WebSocket de reacciones atraviesa el gateway sin código adicional.
func NewGateway(resolver *Resolver) *Gateway {
	g := &Gateway{resolver: resolver}
	g.proxy = &httputil.ReverseProxy{
		Rewrite:        g.rewrite,
		FlushInterval:  -1,
		ModifyResponse: g.modifyResponse,
		ErrorHandler:   g.errorHandler,
	}
	return g
}

// Handler devuelve el http.Handler para una ruta.
func (g *Gateway) Handler(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inst, err := g.resolver.Pick(r.Context(), route.Service)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, ErrNoHealthyInstances) {
				status = http.StatusServiceUnavailable
			}
			log.Printf("[gateway] no se pudo resolver %q: %v", route.Service, err)
			servicekit.WriteJSON(w, status, map[string]string{
				"error":   "servicio no disponible",
				"service": route.Service,
				"detail":  err.Error(),
			})
			return
		}

		up := &upstream{
			route:    route,
			instance: inst,
			target:   &url.URL{Scheme: "http", Host: inst.HostPort()},
		}
		ctx := context.WithValue(r.Context(), upstreamKey{}, up)
		g.proxy.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (g *Gateway) rewrite(pr *httputil.ProxyRequest) {
	up := pr.In.Context().Value(upstreamKey{}).(*upstream)

	pr.SetURL(up.target)
	pr.SetXForwarded()

	// Elimina el prefijo público: /api/audios/x -> /x
	path := strings.TrimPrefix(pr.In.URL.Path, up.route.Prefix)
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	pr.Out.URL.Path = path
	pr.Out.URL.RawPath = ""
	pr.Out.Header.Set("X-Forwarded-Prefix", up.route.Prefix)
}

func (g *Gateway) modifyResponse(res *http.Response) error {
	// El gateway es el único responsable de CORS: se descartan las cabeceras
	// CORS de los upstreams (p. ej. Envoy) para evitar valores duplicados.
	for k := range res.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(k), "Access-Control-") {
			res.Header.Del(k)
		}
	}
	if up, ok := res.Request.Context().Value(upstreamKey{}).(*upstream); ok {
		res.Header.Set("X-Upstream-Service", up.route.Service)
		res.Header.Set("X-Upstream-Instance", up.instance.ID)
	}
	return nil
}

func (g *Gateway) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	up, _ := r.Context().Value(upstreamKey{}).(*upstream)
	if errors.Is(err, context.Canceled) {
		return // el cliente cerró la conexión (normal al detener un stream).
	}
	service := ""
	if up != nil {
		service = up.route.Service
		g.resolver.Invalidate(service) // fuerza a refrescar instancias desde Consul.
	}
	log.Printf("[gateway] error de proxy hacia %q: %v", service, err)
	servicekit.WriteJSON(w, http.StatusBadGateway, map[string]string{
		"error":   "error al contactar el servicio",
		"service": service,
	})
}
