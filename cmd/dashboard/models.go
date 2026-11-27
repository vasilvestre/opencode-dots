package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"

	"opencode-dots/internal/models"
)

const maxModelsBodySize = 16 << 10

// models serves the Config view's model presets (ADR 0052): GET reads model-presets.yaml, PUT
// replaces presets and overrides in it. Workflows read the file when a run starts, so a save
// applies to the next run and nothing else is written.
func (d *dash) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or PUT only"})
		return
	}
	if !d.trustedRequest(r) {
		writeJSONStatus(w, http.StatusForbidden, map[string]string{"error": "refused origin"})
		return
	}
	var req struct {
		Presets   map[string]string  `json:"presets"`
		Overrides map[string]*string `json:"overrides"`
	}
	if r.Method == http.MethodPut && !decodeModelsRequest(w, r, &req) {
		return
	}

	d.editMu.Lock()
	defer d.editMu.Unlock()
	path := filepath.Join(d.root, models.File)
	cfg, err := models.Load(path)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]any{"variables": cfg.Variables(), "presets": cfg.Presets, "available": d.availableModels(r.Context())})
		return
	}
	next, err := cfg.Updated(req.Presets, req.Overrides)
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	data, err := next.Marshal()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	backup, err := backupFile(path)
	if err == nil {
		err = atomicWrite(path, data)
	}
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"changed": []map[string]string{{"path": models.File, "backup": backup}}})
}

// decodeModelsRequest reads the one JSON object of a PUT. It has answered when it returns false.
func decodeModelsRequest(w http.ResponseWriter, r *http.Request, req any) bool {
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || ct != "application/json" {
		writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelsBodySize))
	dec.DisallowUnknownFields()
	err := dec.Decode(req)
	if err == nil {
		if err = dec.Decode(&struct{}{}); errors.Is(err, io.EOF) {
			return true
		}
		err = errors.New("expected one JSON object")
	}
	status := http.StatusBadRequest
	var big *http.MaxBytesError
	if errors.As(err, &big) {
		status = http.StatusRequestEntityTooLarge
	}
	writeJSONStatus(w, status, map[string]string{"error": "bad body: " + err.Error()})
	return false
}

func (d *dash) availableModels(ctx context.Context) []string {
	catalog := d.modelCatalog
	if catalog == nil {
		catalog = models.LoadCatalog
	}
	if ids := catalog(ctx); ids != nil {
		return ids
	}
	return []string{}
}
