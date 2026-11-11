// Servidor de Streaming (Fase 2).
//
// Implementa el servicio gRPC AudioStreamer (proto/audio.proto): recibe un
// AudioRequest { titulo, formato } y transmite el archivo de audio en bloques
// de 65,536 bytes (64 KB) mediante server-streaming.
//
// Expone:
//   - gRPC en SERVICE_PORT (50051): AudioStreamer, grpc.health.v1 y reflection.
//   - HTTP /health en HEALTH_PORT (8081) para diagnóstico y healthchecks.
package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	"transmision/pkg/consul"
	"transmision/pkg/servicekit"
	audiopb "transmision/servicios/streaming/proto"
)

const (
	chunkSize        = 65536 // 64 KB por fragmento
	defaultDummyPath = "audio/sample.mp3"
)

//go:embed audio/*
var embeddedAudio embed.FS

type audioStreamerServer struct {
	audiopb.UnimplementedAudioStreamerServer
	audioDir   string
	chunkDelay time.Duration
}

func (s *audioStreamerServer) StreamAudio(req *audiopb.AudioRequest, stream audiopb.AudioStreamer_StreamAudioServer) error {
	titulo := strings.TrimSpace(req.GetTitulo())
	formato := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.GetFormato())), ".")
	if titulo == "" {
		return status.Error(codes.InvalidArgument, "el campo 'titulo' es obligatorio")
	}
	if formato == "" {
		formato = "mp3"
	}

	rc, sourceDesc, err := s.openAudio(titulo, formato)
	if err != nil {
		return status.Errorf(codes.NotFound, "no se pudo abrir el audio %q (%s): %v", titulo, formato, err)
	}
	defer rc.Close()

	log.Printf("[streaming] iniciando stream: titulo=%q formato=%q origen=%s chunk=%dB delay=%s",
		titulo, formato, sourceDesc, chunkSize, s.chunkDelay)

	ctx := stream.Context()
	buf := make([]byte, chunkSize)
	start := time.Now()
	var totalBytes int64
	var chunkNum int

	for {
		if err := ctx.Err(); err != nil {
			log.Printf("[streaming] cliente desconectado en chunk #%d: %v", chunkNum, err)
			return status.FromContextError(err).Err()
		}

		n, readErr := rc.Read(buf)
		if n > 0 {
			chunkNum++
			totalBytes += int64(n)
			if err := stream.Send(&audiopb.AudioChunk{Data: bytes.Clone(buf[:n])}); err != nil {
				log.Printf("[streaming] error enviando chunk #%d: %v", chunkNum, err)
				return err
			}
			log.Printf("[streaming] enviado chunk #%02d (%d bytes) para %q", chunkNum, n, titulo)

			if s.chunkDelay > 0 {
				timer := time.NewTimer(s.chunkDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return status.FromContextError(ctx.Err()).Err()
				case <-timer.C:
				}
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return status.Errorf(codes.Internal, "error leyendo audio: %v", readErr)
		}
	}

	log.Printf("[streaming] stream completado: titulo=%q chunks=%d total=%d bytes duracion=%s",
		titulo, chunkNum, totalBytes, time.Since(start).Round(time.Millisecond))
	return nil
}

// openAudio busca primero un archivo coincidente en AUDIO_DIR (subidas del Admin),
// luego entre los audios embebidos (cancion1.mp3, podcast1.mp3, etc.) y, si no
// existe ninguno específico, utiliza sample.mp3 como fallback.
func (s *audioStreamerServer) openAudio(titulo, formato string) (io.ReadCloser, string, error) {
	safeTitle := filepath.Base(titulo)
	safeFormat := filepath.Base(formato)
	exactName := fmt.Sprintf("%s.%s", safeTitle, safeFormat)

	if s.audioDir != "" {
		for _, p := range []string{
			filepath.Join(s.audioDir, exactName),
			filepath.Join(s.audioDir, safeTitle),
		} {
			if f, err := os.Open(p); err == nil {
				return f, "disk:" + p, nil
			}
		}
		if entries, err := os.ReadDir(s.audioDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if strings.EqualFold(e.Name(), exactName) || strings.EqualFold(e.Name(), safeTitle) {
					p := filepath.Join(s.audioDir, e.Name())
					if f, openErr := os.Open(p); openErr == nil {
						return f, "disk:" + p, nil
					}
				}
			}
		}
	}

	for _, ep := range []string{
		"audio/" + exactName,
		"audio/" + safeTitle,
		defaultDummyPath,
	} {
		if f, err := embeddedAudio.Open(ep); err == nil {
			return f, "embed:" + ep, nil
		}
	}

	return nil, "", fmt.Errorf("archivo %s no encontrado ni en disco ni embebido", exactName)
}

func main() {
	cfg := servicekit.LoadConfig("streaming", 50051)
	healthPort := servicekit.EnvInt("HEALTH_PORT", 8081)
	chunkDelay := time.Duration(servicekit.EnvInt("CHUNK_DELAY_MS", 25)) * time.Millisecond
	audioDir := servicekit.Env("AUDIO_DIR", "servicios/streaming/audio")

	ctx, stop := servicekit.ShutdownContext()
	defer stop()

	// --- Servidor gRPC ---------------------------------------------------
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		log.Fatalf("[%s] no se pudo abrir el puerto gRPC: %v", cfg.Name, err)
	}

	grpcServer := grpc.NewServer()
	audiopb.RegisterAudioStreamerServer(grpcServer, &audioStreamerServer{
		audioDir:   audioDir,
		chunkDelay: chunkDelay,
	})

	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthSrv)
	reflection.Register(grpcServer)

	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus(cfg.Name, healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("audio.AudioStreamer", healthpb.HealthCheckResponse_SERVING)

	errCh := make(chan error, 2)
	go func() {
		log.Printf("[%s] escuchando gRPC en :%d (AudioStreamer activo, chunk=%dB, delay=%s)",
			cfg.Name, cfg.Port, chunkSize, chunkDelay)
		if err := grpcServer.Serve(lis); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
		}
	}()

	// --- Servidor HTTP de salud -------------------------------------------
	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", healthPort),
		Handler:           servicekit.NewMux(cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("[%s] escuchando HTTP /health en :%d", cfg.Name, healthPort)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()

	// --- Registro en Consul -----------------------------------------------
	grpcCheck := consul.Check{
		CheckID:                        cfg.InstanceID() + "-grpc",
		Name:                           cfg.Name + " gRPC health",
		GRPC:                           fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Interval:                       "10s",
		Timeout:                        "2s",
		DeregisterCriticalServiceAfter: "1m",
	}
	deregister, err := servicekit.Register(ctx, cfg, grpcCheck, servicekit.HTTPCheck(cfg, healthPort))
	if err != nil {
		log.Printf("[%s] ADVERTENCIA: no se pudo registrar en Consul: %v", cfg.Name, err)
	}
	defer deregister()

	// --- Espera y apagado ordenado ------------------------------------------
	select {
	case <-ctx.Done():
		log.Printf("[%s] señal de apagado recibida", cfg.Name)
	case err := <-errCh:
		log.Printf("[%s] error del servidor: %v", cfg.Name, err)
	}

	healthSrv.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)

	done := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
}
