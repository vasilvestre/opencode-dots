package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"sync"

	"opencode-dots/internal/models"
)

const maxModelsBodySize = 16 << 10

var applyModelPlan = func(plan *models.Plan) ([]models.Saved, error) {
	return plan.Apply(backupFile, atomicWrite)
}

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

	update, values, legacy, ok := decodeModelsRequest(w, r)
	if !ok {
		return
	}

	d.editMu.Lock()
	unlock := sync.OnceFunc(d.editMu.Unlock)
	defer unlock()

	snapshot, loadErr := d.readConfiguredModels()
	if snapshot == nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": loadErr.Error()})
		return
	}
	if r.Method == http.MethodGet {
		presets := snapshot.Presets()
		migration := snapshot.MigrationRequired()
		unlock()
		writeJSON(w, map[string]any{
			"variables":          snapshot.Variables(),
			"problems":           modelProblems(snapshot, loadErr),
			"available":          d.availableModels(r.Context()),
			"presets":            presets,
			"migration_required": migration,
		})
		return
	}
	if loadErr != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": loadErr.Error()})
		return
	}

	if legacy {
		if _, err := snapshot.Prepare(values); err != nil {
			writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		overrides := make(map[string]*string, len(values))
		for key, value := range values {
			copied := value
			overrides[key] = &copied
		}
		update = models.Update{Presets: snapshot.Presets(), Overrides: overrides}
	}
	plan, err := snapshot.PrepareUpdate(update)
	if err != nil {
		writeJSONStatus(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	saved, err := applyModelPlan(plan)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"changed": saved})
}

func (d *dash) readConfiguredModels() (*models.Snapshot, error) {
	names := listFiles(filepath.Join(d.root, "workflows"), ".yaml")
	snapshot, err := models.ReadConfigured(names, d.resolveEditable, filepath.Join(d.root, "model-presets.yaml"))
	if err == nil {
		return snapshot, nil
	}
	fallback, fallbackErr := models.Read(names, d.resolveEditable)
	if fallbackErr != nil {
		return nil, err
	}
	return fallback, err
}

func modelProblems(snapshot *models.Snapshot, loadErr error) []string {
	problems := append([]string{}, snapshot.Problems()...)
	if loadErr != nil {
		problems = append(problems, loadErr.Error())
	}
	return problems
}

func decodeModelsRequest(w http.ResponseWriter, r *http.Request) (models.Update, map[string]string, bool, bool) {
	if r.Method != http.MethodPut {
		return models.Update{}, nil, false, true
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeJSONStatus(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/json"})
		return models.Update{}, nil, false, false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModelsBodySize))
	decoder.DisallowUnknownFields()
	var req struct {
		Presets   json.RawMessage `json:"presets"`
		Overrides json.RawMessage `json:"overrides"`
		Variables json.RawMessage `json:"variables"`
	}
	if err := decoder.Decode(&req); err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSONStatus(w, status, map[string]string{"error": "bad body: " + err.Error()})
		return models.Update{}, nil, false, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSONStatus(w, status, map[string]string{"error": "bad body: expected one JSON object"})
		return models.Update{}, nil, false, false
	}

	hasPresets := len(req.Presets) > 0
	hasOverrides := len(req.Overrides) > 0
	hasVariables := len(req.Variables) > 0
	if hasVariables {
		if hasPresets || hasOverrides {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "send either variables or presets with overrides, not both"})
			return models.Update{}, nil, false, false
		}
		var encoded map[string]*string
		if err := json.Unmarshal(req.Variables, &encoded); err != nil || encoded == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "variables must be an object of strings"})
			return models.Update{}, nil, false, false
		}
		values := make(map[string]string, len(encoded))
		for key, value := range encoded {
			if value == nil {
				writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "variables must be an object of strings"})
				return models.Update{}, nil, false, false
			}
			values[key] = *value
		}
		return models.Update{}, values, true, true
	}
	if !hasPresets {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "presets object is required"})
		return models.Update{}, nil, false, false
	}

	var encodedPresets map[string]*string
	if err := json.Unmarshal(req.Presets, &encodedPresets); err != nil || encodedPresets == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "presets must be an object containing light and default strings"})
		return models.Update{}, nil, false, false
	}
	presets := make(map[string]string, len(encodedPresets))
	for key, value := range encodedPresets {
		if value == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "presets must contain light and default strings"})
			return models.Update{}, nil, false, false
		}
		presets[key] = *value
	}
	overrides := map[string]*string{}
	if hasOverrides {
		if err := json.Unmarshal(req.Overrides, &overrides); err != nil || overrides == nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "overrides must be an object of strings or null"})
			return models.Update{}, nil, false, false
		}
	}
	return models.Update{Presets: presets, Overrides: overrides}, nil, false, true
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
