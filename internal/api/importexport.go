package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mia-clark/cloudflared-manager/internal/manager"
	"github.com/mia-clark/cloudflared-manager/pkg/cfdconfig"
)

// ImportExportHandler implements /api/v1/import/* and /api/v1/export/*.
type ImportExportHandler struct {
	m   *manager.Manager
	log *slog.Logger
}

// NewImportExportHandler builds a handler.
func NewImportExportHandler(m *manager.Manager, log *slog.Logger) *ImportExportHandler {
	return &ImportExportHandler{m: m, log: log}
}

// ImportFile handles a multipart upload with a single ".yaml/.yml"
// file in the "file" field. The id is taken from the filename (without
// extension) unless overridden by the "id" form value.
func (h *ImportExportHandler) ImportFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "parse multipart: "+err.Error(), nil)
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "file field required", nil)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "read upload: "+err.Error(), nil)
		return
	}
	id := r.FormValue("id")
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename))
	}
	h.persistRaw(w, id, body)
}

// ImportURL accepts JSON {url, id?} and downloads the config body.
func (h *ImportExportHandler) ImportURL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
		ID  string `json:"id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.URL == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "url required", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	name, data, err := downloadHTTP(ctx, body.URL)
	if err != nil {
		WriteError(w, http.StatusBadGateway, CodeUpstreamFailure, "download failed: "+err.Error(), nil)
		return
	}
	id := body.ID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	}
	h.persistRaw(w, id, data)
}

// ImportText accepts JSON {id, text, format?}.
func (h *ImportExportHandler) ImportText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Format string `json:"format"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ID == "" || body.Text == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "id and text required", nil)
		return
	}
	h.persistRaw(w, body.ID, []byte(body.Text))
}

// ImportZIP accepts a multipart upload containing a zip archive made by
// /export/all. Existing configs are overwritten.
func (h *ImportExportHandler) ImportZIP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "parse multipart: "+err.Error(), nil)
		return
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "file field required", nil)
		return
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "read upload: "+err.Error(), nil)
		return
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "not a valid zip: "+err.Error(), nil)
		return
	}
	imported := []string{}
	for _, zf := range zr.File {
		name := filepath.Base(zf.Name)
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(name, ext)
		if err := h.upsertRaw(id, raw); err != nil {
			h.log.Warn("import zip entry failed", slog.String("entry", name), slog.Any("err", err))
			continue
		}
		imported = append(imported, id)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"imported": imported})
}

// ExportConfig serves the raw config bytes as a download.
func (h *ImportExportHandler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	b, err := h.m.ReadRaw(id)
	if writeManagerError(w, err) {
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.yaml"`, id))
	_, _ = w.Write(b)
}

// ExportAll returns a zip archive of every config file plus meta.json.
func (h *ImportExportHandler) ExportAll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cloudflared-manager-export-%s.zip"`, time.Now().UTC().Format("20060102-150405")))

	zw := zip.NewWriter(w)
	defer zw.Close()

	matches, _ := filepath.Glob(filepath.Join(h.m.ProfilesDir(), "*.yaml"))
	extra, _ := filepath.Glob(filepath.Join(h.m.ProfilesDir(), "*.yml"))
	matches = append(matches, extra...)
	for _, p := range matches {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		fw, err := zw.Create("profiles/" + filepath.Base(p))
		if err != nil {
			continue
		}
		_, _ = fw.Write(raw)
	}
}

// persistRaw upserts a config and replies with the resulting envelope.
func (h *ImportExportHandler) persistRaw(w http.ResponseWriter, id string, raw []byte) {
	if id == "" {
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "id is required", nil)
		return
	}
	if err := h.upsertRaw(id, raw); err != nil {
		if errors.Is(err, manager.ErrNotFound) {
			WriteError(w, http.StatusNotFound, CodeConfigNotFound, err.Error(), nil)
			return
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
		return
	}
	snap, sc, mm, _ := h.m.Get(id)
	WriteJSON(w, http.StatusOK, configEnvelope{Snapshot: snap, Config: sc, Cfdmgr: mm})
}

// upsertRaw creates the config if absent, otherwise replaces its body.
func (h *ImportExportHandler) upsertRaw(id string, raw []byte) error {
	if !h.m.Exists(id) {
		sc, err := cfdconfig.ParseYAML(raw)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}
		return h.m.Create(id, sc, manager.MgrMeta{Name: id})
	}
	return h.m.WriteRaw(id, raw)
}

// downloadHTTP fetches a remote config body. It returns the filename
// suggested by Content-Disposition (or derived from the URL path) and
// the raw bytes.
func downloadHTTP(ctx context.Context, url string) (string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("bad status: %s", resp.Status)
	}
	filename := ""
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			filename = params["filename"]
		}
	}
	if filename == "" {
		filename = path.Base(resp.Request.URL.Path)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return filename, body, err
}
