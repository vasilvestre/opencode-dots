package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"opencode-dots/internal/models"
)

const maxModelsBodySize = 16 << 10

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

	var values map[string]string
	if r.Method == http.MethodPut {
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/json"})
			return
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelsBodySize))
		var req struct {
			Variables json.RawMessage `json:"variables"`
		}
		if err := decoder.Decode(&req); err != nil {
			status := http.StatusBadRequest
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			writeJSONStatus(w, status, map[string]string{"error": "bad body: " + err.Error()})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "bad body: expected one JSON object"})
			return
		}
		if len(req.Variables) == 0 {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "variables object is required"})
			return
		}
		var encoded map[string]*string
		if err := json.Unmarshal(req.Variables, &encoded); err != nil || encoded == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "variables must be an object of strings"})
			return
		}
		values = make(map[string]string, len(encoded))
		for key, value := range encoded {
			if value == nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "variables must be an object of strings"})
				return
			}
			values[key] = *value
		}
	}

	d.editMu.Lock()
	defer d.editMu.Unlock()

	snapshot, err := models.Read(listFiles(filepath.Join(d.root, "workflows"), ".yaml"), d.resolveEditable)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if r.Method == http.MethodGet {
		if len(snapshot.Problems()) > 0 {
			writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": strings.Join(snapshot.Problems(), "; ")})
			return
		}
		writeJSON(w, map[string]any{"variables": snapshot.Variables(), "available": d.availableModels(r.Context())})
		return
	}

	plan, err := snapshot.Prepare(values)
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	saved, err := plan.Apply(backupFile, atomicWrite)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"changed": saved})
}

func (d *dash) availableModels(ctx context.Context) []string {
	catalog := d.modelCatalog
	if catalog == nil {
		catalog = models.LoadCatalog
	}
	ids := catalog(ctx)
	if ids == nil {
		return []string{}
	}
	return ids
}
