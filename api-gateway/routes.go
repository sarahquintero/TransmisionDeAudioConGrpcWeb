package main

// Route asocia un prefijo público del gateway con un servicio de Consul.
// El prefijo se elimina antes de reenviar: /api/audios/123 -> audios:/123
type Route struct {
	Prefix      string `json:"prefix"`
	Service     string `json:"service"`
	Description string `json:"description"`
}

// buildRoutes define la tabla de enrutamiento del API Gateway.
//
// streamingProxy es el nombre en Consul del proxy Envoy que traduce
// gRPC-Web (HTTP/1.1, navegador) a gRPC nativo (HTTP/2) hacia el servidor
// de streaming. El gateway NUNCA habla directamente con el servidor de
// streaming: todo el tráfico de audio pasa por Envoy.
func buildRoutes(streamingProxy string) []Route {
	return []Route{
		{
			Prefix:      "/grpc",
			Service:     streamingProxy,
			Description: "gRPC-Web -> Envoy -> Servidor de Streaming (gRPC)",
		},
		{
			Prefix:      "/api/reacciones",
			Service:     "reacciones",
			Description: "Servidor de Reacciones (REST + WebSocket en /api/reacciones/ws)",
		},
		{
			Prefix:      "/api/suscripciones",
			Service:     "suscripciones",
			Description: "Servidor de Suscripciones",
		},
		{
			Prefix:      "/api/audios",
			Service:     "audios",
			Description: "Servidor de Audios y Metadatos",
		},
		{
			Prefix:      "/api/estadisticas",
			Service:     "estadisticas",
			Description: "Servidor de Estadísticas",
		},
	}
}
