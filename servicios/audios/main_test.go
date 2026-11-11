package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogoSeedMetadatosYRegistro(t *testing.T) {
	store, err := newCatalogStore("", t.TempDir())
	if err != nil {
		t.Fatalf("error cargando seed embebido: %v", err)
	}

	// 1. Todos los audios del seed (5) con metadatos específicos
	recAll := httptest.NewRecorder()
	store.handleList(recAll, httptest.NewRequest(http.MethodGet, "/lista", nil))
	var resAll struct {
		Total  int             `json:"total"`
		Audios []AudioMetadata `json:"audios"`
	}
	if err := json.Unmarshal(recAll.Body.Bytes(), &resAll); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if resAll.Total != 5 {
		t.Fatalf("esperados 5 audios del seed, obtenidos %d", resAll.Total)
	}
	if resAll.Audios[0].Metadatos["artista_principal"] != "Artista Demo" ||
		resAll.Audios[0].Metadatos["album"] != "Album de Prueba" ||
		resAll.Audios[0].Metadatos["genero_musical"] != "Rock" {
		t.Fatalf("metadatos inesperados para cancion1: %+v", resAll.Audios[0].Metadatos)
	}
	if resAll.Audios[2].Metadatos["anfitrion"] != "Profesor X" {
		t.Fatalf("metadatos inesperados para podcast1: %+v", resAll.Audios[2].Metadatos)
	}

	// 2. Filtro tolerante a acentos (?tipo=Musica -> Música)
	recMus := httptest.NewRecorder()
	store.handleList(recMus, httptest.NewRequest(http.MethodGet, "/lista?tipo=Musica", nil))
	var resMus struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(recMus.Body.Bytes(), &resMus)
	if resMus.Total != 2 {
		t.Fatalf("esperados 2 audios de Música, obtenidos %d", resMus.Total)
	}

	// 3. Registrar un nuevo audio Podcast con metadatos anidados
	payload := `{
		"titulo": "episodio2",
		"tipo": "Podcast",
		"formato": "mp3",
		"archivo": "episodio2.mp3",
		"metadatos": {
			"nombre_podcast": "Sistemas Distribuidos",
			"titulo_episodio": "Envoy y gRPC-Web",
			"anfitrion": "Dra. Ada"
		}
	}`
	regReq := httptest.NewRequest(http.MethodPost, "/registrar", bytes.NewBufferString(payload))
	regRec := httptest.NewRecorder()
	store.handleRegister(regRec, regReq)
	if regRec.Code != http.StatusCreated {
		t.Fatalf("esperado 201 en /registrar, obtenido %d: %s", regRec.Code, regRec.Body.String())
	}

	var regBody struct {
		Audio AudioMetadata `json:"audio"`
	}
	_ = json.Unmarshal(regRec.Body.Bytes(), &regBody)
	if regBody.Audio.Metadatos["titulo_episodio"] != "Envoy y gRPC-Web" {
		t.Fatalf("no se preservaron los metadatos al registrar: %+v", regBody.Audio.Metadatos)
	}

	recPod := httptest.NewRecorder()
	store.handleList(recPod, httptest.NewRequest(http.MethodGet, "/lista?tipo=Podcast", nil))
	var resPod struct {
		Total int `json:"total"`
	}
	_ = json.Unmarshal(recPod.Body.Bytes(), &resPod)
	if resPod.Total != 2 {
		t.Fatalf("esperados 2 podcasts tras registrar, obtenidos %d", resPod.Total)
	}
}

func TestMultipartUploadGuardaArchivoEnDisco(t *testing.T) {
	uploadDir := t.TempDir()
	store, err := newCatalogStore("", uploadDir)
	if err != nil {
		t.Fatalf("error inicializando store: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("titulo", "Bitter Sweet Symphony")
	_ = mw.WriteField("tipo", "Música")
	_ = mw.WriteField("formato", "mp3")
	_ = mw.WriteField("metadatos", `{"artista_principal":"The Verve","album":"Urban Hymns","genero_musical":"Britpop"}`)
	fw, _ := mw.CreateFormFile("archivo_binario", "Bitter Sweet Symphony.mp3")
	_, _ = fw.Write([]byte("FAKE-MP3-PAYLOAD-123456"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/registrar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	store.handleRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("esperado 201, obtenido %d: %s", rec.Code, rec.Body.String())
	}

	savedPath := filepath.Join(uploadDir, "Bitter Sweet Symphony.mp3")
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("archivo no guardado en %s: %v", savedPath, err)
	}
	if string(data) != "FAKE-MP3-PAYLOAD-123456" {
		t.Fatalf("contenido guardado inesperado: %q", string(data))
	}
}
