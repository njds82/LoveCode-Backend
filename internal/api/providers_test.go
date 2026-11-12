package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lovecode/lovecode/internal/audit"
	"github.com/lovecode/lovecode/internal/config"
	"github.com/lovecode/lovecode/internal/models"
	"github.com/lovecode/lovecode/internal/providers"
)

func (f *fixture) doJSON(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = loop
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) loadProviders(t *testing.T, configs ...config.ProviderConfig) {
	t.Helper()
	if err := f.manager.Load(context.Background(), configs); err != nil {
		t.Fatalf("load providers: %v", err)
	}
}

func testProviderConfig() config.ProviderConfig {
	return config.ProviderConfig{
		ID:          "mock",
		Type:        "mock",
		DisplayName: "Mock Provider",
		Endpoint:    "https://example.test/v1?token=top-secret",
		Models: []config.ModelConfig{
			{ID: "mock:alpha", ProviderModelID: "alpha", DisplayName: "Alpha", Tags: []string{"fast"}},
			{ID: "mock:beta/model", ProviderModelID: "beta/model", DisplayName: "Beta"},
		},
		Settings: map[string]any{"reply": "hello"},
	}
}

func TestProviderManagementAuthAndValidation(t *testing.T) {
	f := setup(t, nil)
	f.loadProviders(t, testProviderConfig())
	readTok, _, err := f.tokens.Create(context.Background(), "reader", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	if rec := f.do("GET", "/v1/providers", "", loop); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: %d", rec.Code)
	}
	if rec := f.do("GET", "/v1/providers", readTok, loop); rec.Code != http.StatusOK {
		t.Fatalf("read list: %d %s", rec.Code, rec.Body)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", readTok, `{"enabled":false}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read patch must be forbidden: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, ``); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, `{"enabled":false,"extra":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, `{"enabled":false}{"enabled":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("trailing value: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/missing", f.admin, `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing provider: %d", rec.Code)
	}
}

func TestProviderManagementFlowAndAudit(t *testing.T) {
	f := setup(t, nil)
	f.loadProviders(t, testProviderConfig())

	rec := f.do("GET", "/v1/providers", f.admin, loop)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Providers []providers.ProviderStatus `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 1 || list.Providers[0].ID != "mock" || list.Providers[0].ModelCount != 2 {
		t.Fatalf("providers: %+v", list.Providers)
	}
	if list.Providers[0].Endpoint != "https://example.test/v1" {
		t.Fatalf("endpoint not sanitized: %+v", list.Providers[0])
	}
	if strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatalf("list leaked endpoint secret: %s", rec.Body)
	}

	if rec := f.do("GET", "/v1/providers/mock", f.admin, loop); rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("GET", "/v1/providers/missing", f.admin, loop); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing: %d", rec.Code)
	}

	rec = f.doJSON("PATCH", "/v1/providers/mock", f.admin, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body)
	}
	var status providers.ProviderStatus
	json.Unmarshal(rec.Body.Bytes(), &status)
	if status.Enabled {
		t.Fatalf("disable did not apply: %+v", status)
	}
	if _, err := f.manager.Registry().Chat(context.Background(), "mock", providers.ChatRequest{}); !errors.Is(err, providers.ErrProviderDisabled) {
		t.Fatalf("disabled provider accepted chat: %v", err)
	}

	if rec := f.do("POST", "/v1/providers/mock/test", f.admin, loop); rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body)
	}
	rec = f.do("POST", "/v1/providers/mock/models/refresh", f.admin, loop)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", rec.Code, rec.Body)
	}
	var refresh providers.RefreshResult
	json.Unmarshal(rec.Body.Bytes(), &refresh)
	if refresh.Updated != 2 || refresh.Total != 2 {
		t.Fatalf("refresh body: %+v", refresh)
	}
	if rec := f.do("POST", "/v1/providers/missing/test", f.admin, loop); rec.Code != http.StatusNotFound {
		t.Fatalf("test missing: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", rec.Code, rec.Body)
	}

	for _, action := range []string{"provider.disabled", "provider.test", "provider.refresh_models", "provider.enabled"} {
		rec := f.do("GET", "/v1/audit?action="+action, f.admin, loop)
		if rec.Code != http.StatusOK {
			t.Fatalf("audit %s: %d", action, rec.Code)
		}
		var body struct{ Entries []audit.Entry }
		json.Unmarshal(rec.Body.Bytes(), &body)
		var found bool
		for _, entry := range body.Entries {
			if entry.Target == "mock" {
				found = true
				if entry.Actor != "owner" {
					t.Fatalf("audit %s actor: %+v", action, entry)
				}
			}
		}
		if !found {
			t.Fatalf("audit %s missing mock entry: %+v", action, body.Entries)
		}
	}
}

func TestProviderTestFailureIsNormalized(t *testing.T) {
	f := setup(t, nil)
	cfg := testProviderConfig()
	cfg.Settings = map[string]any{"unavailable": true}
	f.loadProviders(t, cfg)

	rec := f.do("POST", "/v1/providers/mock/test", f.admin, loop)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable test: %d %s", rec.Code, rec.Body)
	}
	var body errorBody
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != string(providers.KindUnavailable) || body.Error.RequestID == "" {
		t.Fatalf("error body: %+v", body)
	}
	if rec := f.do("POST", "/v1/providers/missing/refresh", f.admin, loop); rec.Code != http.StatusNotFound {
		t.Fatalf("refresh missing: %d", rec.Code)
	}
}

func TestModelManagementEndpoints(t *testing.T) {
	f := setup(t, nil)
	f.loadProviders(t, testProviderConfig())
	readTok, _, err := f.tokens.Create(context.Background(), "reader", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	if rec := f.do("GET", "/v1/models", "", loop); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated models: %d", rec.Code)
	}
	rec := f.do("GET", "/v1/models", readTok, loop)
	if rec.Code != http.StatusOK {
		t.Fatalf("models: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Models []models.Model `json:"models"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Models) != 2 {
		t.Fatalf("models: %+v", list.Models)
	}
	if rec := f.do("GET", "/v1/models?tag=fast", readTok, loop); rec.Code != http.StatusOK {
		json.Unmarshal(rec.Body.Bytes(), &list)
		if len(list.Models) != 1 || list.Models[0].ID != "mock:alpha" {
			t.Fatalf("tag filter: %+v", list.Models)
		}
	}
	if rec := f.do("GET", "/v1/models?provider_id=other", readTok, loop); rec.Code != http.StatusOK {
		json.Unmarshal(rec.Body.Bytes(), &list)
		if len(list.Models) != 0 {
			t.Fatalf("provider filter: %+v", list.Models)
		}
	}
	if rec := f.do("GET", "/v1/models?enabled=false", readTok, loop); rec.Code != http.StatusOK {
		json.Unmarshal(rec.Body.Bytes(), &list)
		if len(list.Models) != 0 {
			t.Fatalf("enabled filter: %+v", list.Models)
		}
	}
	if rec := f.do("GET", "/v1/models?availability=available", readTok, loop); rec.Code != http.StatusOK {
		json.Unmarshal(rec.Body.Bytes(), &list)
		if len(list.Models) != 0 {
			t.Fatalf("availability filter: %+v", list.Models)
		}
	}
	if rec := f.do("GET", "/v1/models?enabled=maybe", readTok, loop); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad enabled: %d", rec.Code)
	}
	if rec := f.do("GET", "/v1/models?availability=sometimes", readTok, loop); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad availability: %d", rec.Code)
	}

	for _, id := range []string{"mock:alpha", "mock:beta/model"} {
		rec := f.do("GET", "/v1/models/"+id, readTok, loop)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %s: %d %s", id, rec.Code, rec.Body)
		}
		var model models.Model
		json.Unmarshal(rec.Body.Bytes(), &model)
		if model.ID != id {
			t.Fatalf("get %s returned %+v", id, model)
		}
	}
	if rec := f.do("GET", "/v1/models/missing", readTok, loop); rec.Code != http.StatusNotFound {
		t.Fatalf("missing model: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/models/mock:alpha", readTok, `{"enabled":false}`); rec.Code != http.StatusForbidden {
		t.Fatalf("read model patch: %d", rec.Code)
	}
	rec = f.doJSON("PATCH", "/v1/models/mock:alpha", f.admin, `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable model: %d %s", rec.Code, rec.Body)
	}
	var model models.Model
	json.Unmarshal(rec.Body.Bytes(), &model)
	if model.Enabled || model.Availability != models.AvailabilityUnknown {
		t.Fatalf("model patch: %+v", model)
	}
	if rec := f.doJSON("PATCH", "/v1/models/missing", f.admin, `{"enabled":false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing model patch: %d", rec.Code)
	}
	if rec := f.doJSON("PATCH", "/v1/models/mock:alpha", f.admin, `{"enabled":false,"x":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("model unknown field: %d", rec.Code)
	}

	rec = f.do("GET", "/v1/audit?action=model.disabled", f.admin, loop)
	if rec.Code != http.StatusOK {
		t.Fatalf("model audit: %d", rec.Code)
	}
	var entries struct{ Entries []audit.Entry }
	json.Unmarshal(rec.Body.Bytes(), &entries)
	if len(entries.Entries) != 1 || entries.Entries[0].Target != "mock:alpha" {
		t.Fatalf("model audit entries: %+v", entries.Entries)
	}
}

func TestManagementBodyLimit(t *testing.T) {
	f := setup(t, func(c *config.Server) { c.MaxBodyBytes = 40 })
	f.loadProviders(t, testProviderConfig())
	body := `{"enabled":false,"padding":"` + strings.Repeat("x", 200) + `"}`
	rec := f.doJSON("PATCH", "/v1/providers/mock", f.admin, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit: %d %s", rec.Code, rec.Body)
	}
	var e errorBody
	json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Error.Code != "body_too_large" {
		t.Fatalf("body limit error: %+v", e)
	}
}

func TestProviderEndpointsUnavailableWithoutManager(t *testing.T) {
	f := setup(t, nil)
	f.srv.providers = nil
	rec := f.do("GET", "/v1/providers", f.admin, loop)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no manager: %d", rec.Code)
	}
}
