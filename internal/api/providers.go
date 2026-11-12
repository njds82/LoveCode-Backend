package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
)

// ---- provider management ----

func (s *Server) handleProviderList(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": s.providers.ProviderStatuses()})
}

func (s *Server) handleProviderGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	id := r.PathValue("id")
	st, err := s.providers.Status(id)
	if err != nil {
		s.writeManagementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type providerPatchRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleProviderPatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	var body providerPatchRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if body.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled is required")
		return
	}
	id := r.PathValue("id")
	if err := s.providers.SetProviderEnabled(id, *body.Enabled); err != nil {
		s.writeManagementError(w, r, err)
		return
	}
	st, err := s.providers.Status(id)
	if err != nil {
		s.writeManagementError(w, r, err)
		return
	}
	action := "provider.disabled"
	if *body.Enabled {
		action = "provider.enabled"
	}
	s.recordAudit(r, action, id, map[string]any{"enabled": *body.Enabled})
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	id := r.PathValue("id")
	err := s.providers.TestProvider(r.Context(), id)
	detail := map[string]any{"ok": err == nil}
	if err != nil {
		if pe := providerError(err); pe != nil {
			detail["kind"] = pe.Kind
		}
	}
	s.recordAudit(r, "provider.test", id, detail)
	if err != nil {
		s.writeProviderFailure(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"provider_id": id, "status": "ok"})
}

func (s *Server) handleProviderRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	id := r.PathValue("id")
	res, err := s.providers.RefreshModels(r.Context(), id)
	if err != nil {
		s.recordAudit(r, "provider.refresh_models", id, map[string]any{"ok": false})
		s.writeProviderFailure(w, r, err)
		return
	}
	s.recordAudit(r, "provider.refresh_models", id, map[string]any{
		"ok": true, "added": res.Added, "updated": res.Updated, "total": res.Total,
	})
	writeJSON(w, http.StatusOK, res)
}

// ---- model management ----

func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	q := r.URL.Query()
	enabled, err := parseOptionalBool(q.Get("enabled"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled must be true or false")
		return
	}
	var availability *models.Availability
	if raw := q.Get("availability"); raw != "" {
		a := models.Availability(raw)
		if !a.Valid() {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "availability must be unknown, available or unavailable")
			return
		}
		availability = &a
	}
	list := s.providers.ModelRegistry().List(models.Filter{
		ProviderID:   q.Get("provider_id"),
		Tag:          q.Get("tag"),
		Enabled:      enabled,
		Availability: availability,
	})
	writeJSON(w, http.StatusOK, map[string]any{"models": list})
}

func (s *Server) handleModelGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	model, err := s.providers.ModelRegistry().Get(r.PathValue("id"))
	if err != nil {
		s.writeManagementError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

type modelPatchRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleModelPatch(w http.ResponseWriter, r *http.Request) {
	if !s.requireProviders(w, r) {
		return
	}
	var body modelPatchRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		writeJSONBodyError(w, r, err)
		return
	}
	if body.Enabled == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "enabled is required")
		return
	}
	model, err := s.providers.ModelRegistry().SetEnabled(r.PathValue("id"), *body.Enabled)
	if err != nil {
		s.writeManagementError(w, r, err)
		return
	}
	action := "model.disabled"
	if *body.Enabled {
		action = "model.enabled"
	}
	s.recordAudit(r, action, model.ID, map[string]any{"enabled": *body.Enabled})
	writeJSON(w, http.StatusOK, model)
}

// ---- helpers ----

func (s *Server) requireProviders(w http.ResponseWriter, r *http.Request) bool {
	if s.providers != nil {
		return true
	}
	writeError(w, r, http.StatusServiceUnavailable, "providers_unavailable", "provider management is not configured")
	return false
}

func (s *Server) recordAudit(r *http.Request, action, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	actor := "anonymous"
	if p := PrincipalFrom(r.Context()); p != nil {
		actor = p.Name
	}
	if err := s.audit.Append(r.Context(), audit.Event{
		Actor: actor, Action: action, Target: target,
		CorrelationID: RequestID(r.Context()), Detail: detail,
	}); err != nil {
		s.log.Error("audit append failed", "action", action, "err", err)
	}
}

func decodeStrictJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("body must contain exactly one JSON value")
	default:
		return err
	}
}

func writeJSONBodyError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
		return
	}
	writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid request body")
}

func parseOptionalBool(raw string) (*bool, error) {
	switch raw {
	case "":
		return nil, nil
	case "true":
		v := true
		return &v, nil
	case "false":
		v := false
		return &v, nil
	default:
		return nil, errors.New("not a boolean")
	}
}

func providerError(err error) *providers.Error {
	var pe *providers.Error
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}

func (s *Server) writeProviderFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, providers.ErrProviderNotFound), errors.Is(err, models.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
		return
	case errors.Is(err, providers.ErrProviderBusy):
		writeError(w, r, http.StatusConflict, "provider_busy", "provider has requests in flight")
		return
	case errors.Is(err, providers.ErrProviderDisabled):
		writeError(w, r, http.StatusConflict, "provider_disabled", "provider is disabled")
		return
	case errors.Is(err, providers.ErrDuplicateProvider), errors.Is(err, models.ErrDuplicate):
		writeError(w, r, http.StatusConflict, "already_exists", "resource already exists")
		return
	}
	if pe := providerError(err); pe != nil {
		if pe.RetryAfter > 0 {
			seconds := int(math.Ceil(pe.RetryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		writeError(w, r, providerErrorStatus(pe.Kind), string(pe.Kind), pe.Message)
		return
	}
	writeError(w, r, http.StatusInternalServerError, "internal", "provider operation failed")
}

func providerErrorStatus(kind providers.Kind) int {
	switch kind {
	case providers.KindInvalidRequest:
		return http.StatusBadRequest
	case providers.KindUnsupported:
		return http.StatusBadRequest
	case providers.KindModelNotFound:
		return http.StatusNotFound
	case providers.KindUnavailable, providers.KindConnection:
		return http.StatusServiceUnavailable
	case providers.KindRateLimited, providers.KindQuotaExceeded:
		return http.StatusTooManyRequests
	case providers.KindCanceled:
		return http.StatusRequestTimeout
	case providers.KindTimeout:
		return http.StatusGatewayTimeout
	case providers.KindInvalidConfig:
		return http.StatusInternalServerError
	default:
		return http.StatusBadGateway
	}
}

func (s *Server) writeManagementError(w http.ResponseWriter, r *http.Request, err error) {
	s.writeProviderFailure(w, r, err)
}
