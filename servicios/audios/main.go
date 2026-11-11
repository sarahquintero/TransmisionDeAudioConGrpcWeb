// Servidor de Audios y Metadatos (Fase 4).
//
// Inicializa el catálogo leyendo seed.json (con metadatos anidados por tipo) y expone:
//   - GET  /lista?tipo={tipo}  -> lista de audios filtrada por tipo
//   - POST /registrar          -> registra un nuevo audio con metadatos (JSON o multipart/form-data)
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"transmision/pkg/servicekit"
)

//go:embed seed.json
var embeddedSeed []byte

// AudioMetadata describe un elemento del catálogo de audio con sus metadatos específicos.
type AudioMetadata struct {
	ID        string            `json:"id"`
	Titulo    string            `json:"titulo"`
	Tipo      string            `json:"tipo"`
	Formato   string            `json:"formato"`
	Archivo   string            `json:"archivo"`
	Metadatos map[string]string `json:"metadatos,omitempty"`
}

type catalogStore struct {
	mu        sync.RWMutex
	audios    []AudioMetadata
	uploadDir string
}

func newCatalogStore(seedPath, uploadDir string) (*catalogStore, error) {
	raw := embeddedSeed
	source := "embed:seed.json"
	if seedPath != "" {
		if data, err := os.ReadFile(seedPath); err == nil {
			raw = data
			source = "file:" + seedPath
		}
	}

	var items []AudioMetadata
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("error parseando %s: %w", source, err)
	}
	log.Printf("[audios] catálogo inicializado con %d elementos desde %s", len(items), source)

	return &catalogStore{
		audios:    items,
		uploadDir: uploadDir,
	}, nil
}

func (s *catalogStore) list(tipo string) []AudioMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()

	normFilter := normalizeText(tipo)
	if normFilter == "" || normFilter == "todos" {
		out := make([]AudioMetadata, len(s.audios))
		copy(out, s.audios)
		return out
	}

	out := make([]AudioMetadata, 0, len(s.audios))
	for _, a := range s.audios {
		if normalizeText(a.Tipo) == normFilter {
			out = append(out, a)
		}
	}
	return out
}

func (s *catalogStore) add(item AudioMetadata) AudioMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()

	if item.ID == "" {
		item.ID = fmt.Sprintf("%s-%d", sanitizeID(item.Titulo), time.Now().UnixMilli())
	}
	if item.Metadatos == nil {
		item.Metadatos = map[string]string{}
	}
	s.audios = append(s.audios, item)
	return item
}

func (s *catalogStore) handleList(w http.ResponseWriter, r *http.Request) {
	tipo := r.URL.Query().Get("tipo")
	items := s.list(tipo)
	servicekit.WriteJSON(w, http.StatusOK, map[string]any{
		"tipo":   tipo,
		"total":  len(items),
		"audios": items,
	})
}

func (s *catalogStore) handleRegister(w http.ResponseWriter, r *http.Request) {
	var item AudioMetadata
	ct := r.Header.Get("Content-Type")

	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "multipart inválido: " + err.Error()})
			return
		}
		item.Titulo = strings.TrimSpace(r.FormValue("titulo"))
		item.Tipo = strings.TrimSpace(r.FormValue("tipo"))
		item.Formato = strings.TrimSpace(r.FormValue("formato"))
		item.Archivo = strings.TrimSpace(r.FormValue("archivo"))

		if rawMeta := strings.TrimSpace(r.FormValue("metadatos")); rawMeta != "" {
			var parsed map[string]string
			if err := json.Unmarshal([]byte(rawMeta), &parsed); err != nil {
				servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "metadatos JSON inválidos: " + err.Error()})
				return
			}
			item.Metadatos = cleanMetaMap(parsed)
		}

		// También admite campos prefijados con meta_ en el formulario.
		for k, vals := range r.MultipartForm.Value {
			if strings.HasPrefix(k, "meta_") && len(vals) > 0 {
				key := strings.TrimPrefix(k, "meta_")
				if v := strings.TrimSpace(vals[0]); v != "" {
					if item.Metadatos == nil {
						item.Metadatos = map[string]string{}
					}
					item.Metadatos[key] = v
				}
			}
		}

		file, header, err := r.FormFile("archivo_binario")
		if err != nil {
			file, header, err = r.FormFile("file")
		}
		if err == nil && file != nil {
			defer file.Close()
			baseName := filepath.Base(header.Filename)
			if item.Archivo == "" {
				item.Archivo = baseName
			}
			// Derivamos título/formato antes de guardar para que el nombre en disco coincida con StreamAudio.
			tituloParaArchivo := item.Titulo
			if tituloParaArchivo == "" {
				tituloParaArchivo = strings.TrimSuffix(baseName, filepath.Ext(baseName))
			}
			if s.uploadDir != "" {
				if mkErr := os.MkdirAll(s.uploadDir, 0o755); mkErr != nil {
					log.Printf("[audios] error creando directorio %s: %v", s.uploadDir, mkErr)
					servicekit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo preparar el directorio de audio: " + mkErr.Error()})
					return
				}
				dstName := sanitizeFileBase(tituloParaArchivo, item.Formato, baseName)
				dstPath := filepath.Join(s.uploadDir, dstName)
				dst, createErr := os.Create(dstPath)
				if createErr != nil {
					log.Printf("[audios] error creando archivo %s: %v", dstPath, createErr)
					servicekit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "no se pudo guardar el archivo de audio: " + createErr.Error()})
					return
				}
				written, copyErr := io.Copy(dst, file)
				closeErr := dst.Close()
				if copyErr != nil || closeErr != nil {
					_ = os.Remove(dstPath)
					log.Printf("[audios] error escribiendo archivo %s: copy=%v close=%v", dstPath, copyErr, closeErr)
					servicekit.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "error al escribir el archivo de audio"})
					return
				}
				if written == 0 {
					_ = os.Remove(dstPath)
					servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "el archivo subido está vacío (0 bytes)"})
					return
				}
				item.Archivo = dstName
				log.Printf("[audios] archivo guardado en %s (%d bytes)", dstPath, written)
			}
		}
	} else {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&item); err != nil {
			servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido: " + err.Error()})
			return
		}
		item.Metadatos = cleanMetaMap(item.Metadatos)
	}

	item.Archivo = filepath.Base(strings.TrimSpace(item.Archivo))
	if item.Archivo == "." || item.Archivo == "/" {
		item.Archivo = ""
	}
	if item.Titulo == "" && item.Archivo != "" {
		item.Titulo = strings.TrimSuffix(item.Archivo, filepath.Ext(item.Archivo))
	}
	if item.Formato == "" && item.Archivo != "" {
		item.Formato = strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Archivo)), ".")
	}
	if item.Formato == "" {
		item.Formato = "mp3"
	}
	if item.Tipo == "" {
		item.Tipo = "Música"
	}
	if item.Titulo == "" {
		servicekit.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "se requiere 'titulo' o 'archivo'"})
		return
	}
	if item.Archivo == "" {
		item.Archivo = item.Titulo + "." + item.Formato
	}

	created := s.add(item)
	log.Printf("[audios] registrado nuevo audio: id=%q titulo=%q tipo=%q archivo=%q metadatos=%v",
		created.ID, created.Titulo, created.Tipo, created.Archivo, created.Metadatos)

	servicekit.WriteJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"mensaje": "Audio registrado correctamente",
		"audio":   created,
	})
}

func cleanMetaMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

func sanitizeFileBase(titulo, formato, fallback string) string {
	if titulo != "" {
		ext := strings.TrimPrefix(strings.ToLower(formato), ".")
		if ext == "" {
			ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(fallback)), ".")
		}
		if ext == "" {
			ext = "mp3"
		}
		return filepath.Base(titulo) + "." + ext
	}
	return filepath.Base(fallback)
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "audio"
	}
	return b.String()
}

// normalizeText pasa a minúsculas y quita acentos para comparar tipos ("Musica" == "Música").
func normalizeText(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 'á', 'à', 'ä', 'â':
			b.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			b.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			b.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô':
			b.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			b.WriteRune('u')
		default:
			if !unicode.Is(unicode.Mn, r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func main() {
	cfg := servicekit.LoadConfig("audios", 8084)
	seedPath := servicekit.Env("SEED_FILE", "servicios/audios/seed.json")
	uploadDir := servicekit.Env("AUDIO_UPLOAD_DIR", "/data/audio")

	store, err := newCatalogStore(seedPath, uploadDir)
	if err != nil {
		log.Fatalf("[%s] %v", cfg.Name, err)
	}

	mux := servicekit.NewMux(cfg)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		servicekit.WriteJSON(w, http.StatusOK, map[string]string{
			"service": cfg.Name,
			"message": "Servidor de Audios y Metadatos activo",
		})
	})
	mux.HandleFunc("GET /lista", store.handleList)
	mux.HandleFunc("POST /registrar", store.handleRegister)

	servicekit.RunHTTP(cfg, mux)
}
