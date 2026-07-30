package moxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMappingTemplateAndRequestJournal(t *testing.T) {
	ms := NewMockServer()
	defer ms.Close()

	err := ms.AddMapping(Mapping{
		Priority: 1,
		Request: MappingRequest{
			Method: "POST",
			Path:   "/users/{id}",
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			QueryMatches: map[string]ValueMatcher{
				"trace": {Matches: "^abc-[0-9]+$"},
			},
			Cookies: map[string]ValueMatcher{
				"session": {EqualTo: "s1"},
			},
			BasicAuth: &BasicAuth{Username: "user", Password: "pass"},
			JSONFields: map[string]interface{}{
				"profile.name": "Alice",
			},
		},
		Response: &MappingResponse{
			Status:       201,
			BodyTemplate: `{"id":"{{index .PathVariables "id"}}","name":"{{jsonPath "profile.name"}}","trace":"{{first (index .Query "trace")}}"}`,
			HeaderTemplates: map[string]string{
				"X-User": `{{index .PathVariables "id"}}`,
			},
		},
	})
	if err != nil {
		t.Fatalf("AddMapping failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, ms.URL()+"/users/42?trace=abc-123", strings.NewReader(`{"profile":{"name":"Alice"}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("user", "pass")
	req.AddCookie(&http.Cookie{Name: "session", Value: "s1"})
	req.Header.Set("Content-Type", "application/json")

	resp, err := ms.DefaultClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", resp.StatusCode, string(body))
	}
	if resp.Header.Get("X-User") != "42" {
		t.Fatalf("expected templated response header")
	}
	if !strings.Contains(string(body), `"name":"Alice"`) {
		t.Fatalf("expected templated response body, got %s", string(body))
	}

	requests := ms.GetRequests()
	if len(requests) != 1 || !requests[0].Matched {
		t.Fatalf("expected one matched request, got %+v", requests)
	}
	if ms.ExpectationCallCount(ms.expectations[0]) != 1 {
		t.Fatalf("expected call count 1")
	}
}

func TestLoadMappingsSequentialResponses(t *testing.T) {
	dir := t.TempDir()
	mappingFile := filepath.Join(dir, "status.json")
	mapping := `{
		"request": {"method": "GET", "path": "/status"},
		"responses": [
			{"status": 202, "body": "pending"},
			{"status": 200, "body": "done"}
		],
		"times": 2
	}`
	if err := os.WriteFile(mappingFile, []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := NewMockServer()
	defer ms.Close()
	if err := ms.LoadMappings(dir); err != nil {
		t.Fatalf("LoadMappings failed: %v", err)
	}

	resp1, err := http.Get(ms.URL() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()

	resp2, err := http.Get(ms.URL() + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()

	if resp1.StatusCode != 202 || string(body1) != "pending" {
		t.Fatalf("unexpected first response: %d %s", resp1.StatusCode, string(body1))
	}
	if resp2.StatusCode != 200 || string(body2) != "done" {
		t.Fatalf("unexpected second response: %d %s", resp2.StatusCode, string(body2))
	}
	if err := ms.VerifyExpectations(); err != nil {
		t.Fatalf("expected calls to satisfy times: %v", err)
	}
}

func TestLoadMappingsSortsByPriority(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"z-high-priority.json": `{
			"name": "high",
			"priority": 1,
			"request": {"method": "GET", "path": "/priority"},
			"response": {"status": 200, "body": "high"}
		}`,
		"a-default-priority.json": `{
			"name": "default",
			"request": {"method": "GET", "path": "/priority"},
			"response": {"status": 200, "body": "default"}
		}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ms := NewMockServer()
	defer ms.Close()
	if err := ms.LoadMappings(dir); err != nil {
		t.Fatalf("LoadMappings failed: %v", err)
	}

	if len(ms.mappings) != 2 || ms.mappings[0].Name != "high" {
		t.Fatalf("expected mappings to be registered by priority, got %+v", ms.mappings)
	}

	resp, err := http.Get(ms.URL() + "/priority")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "high" {
		t.Fatalf("expected high-priority mapping response, got %s", string(body))
	}
}

func TestLoadMappingsBodyFileMustStayInsideMappingDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "mappings")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	mapping := `{
		"request": {"method": "GET", "path": "/secret"},
		"response": {"status": 200, "bodyFile": "../secret.txt"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "secret.json"), []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := NewMockServer()
	defer ms.Close()
	err := ms.LoadMappings(dir)
	if err == nil || !strings.Contains(err.Error(), "escapes mapping directory") {
		t.Fatalf("expected bodyFile escape error, got %v", err)
	}
}

func TestLoadMappingsBodyFileRelativeToMappingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "body.txt"), []byte("from-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	mapping := `{
		"request": {"method": "GET", "path": "/file"},
		"response": {"status": 200, "bodyFile": "body.txt"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "file.json"), []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}

	ms := NewMockServer()
	defer ms.Close()
	if err := ms.LoadMappings(dir); err != nil {
		t.Fatalf("LoadMappings failed: %v", err)
	}

	resp, err := http.Get(ms.URL() + "/file")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "from-file" {
		t.Fatalf("expected body file response, got %s", string(body))
	}
}

func TestAdminAPI(t *testing.T) {
	ms := NewMockServer()
	defer ms.Close()

	resp, err := http.Get(ms.URL() + "/__moxy/health")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected health 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	mapping := Mapping{
		Request:  MappingRequest{Method: "GET", Path: "/admin-added"},
		Response: &MappingResponse{Status: 200, Body: "ok"},
	}
	data, _ := json.Marshal(mapping)
	resp, err = http.Post(ms.URL()+"/__moxy/mappings", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected create 201, got %d: %s", resp.StatusCode, string(body))
	}
	_ = resp.Body.Close()

	resp, err = http.Get(ms.URL() + "/admin-added")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("expected admin mapping response, got %s", string(body))
	}

	resp, err = http.Get(ms.URL() + "/__moxy/requests")
	if err != nil {
		t.Fatal(err)
	}
	var requests []RequestRecord
	if err := json.NewDecoder(resp.Body).Decode(&requests); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(requests) != 1 || requests[0].Path != "/admin-added" {
		t.Fatalf("expected one recorded mock request, got %+v", requests)
	}
}

func TestMappingHelpersAndErrors(t *testing.T) {
	t.Run("to expectation validation", func(t *testing.T) {
		_, err := (Mapping{}).ToExpectation()
		if err == nil {
			t.Fatal("expected validation error")
		}

		_, err = (Mapping{
			Request: MappingRequest{Method: "GET", PathPattern: "["},
			Response: &MappingResponse{
				Status: 200,
				Body:   "ok",
			},
		}).ToExpectation()
		if err == nil {
			t.Fatal("expected invalid regex error")
		}
	})

	t.Run("load mappings errors", func(t *testing.T) {
		ms := NewMockServer()
		defer ms.Close()

		if err := ms.LoadMappings(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected missing directory error")
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ms.LoadMappings(dir); err == nil {
			t.Fatal("expected invalid json error")
		}
	})

	t.Run("response body file and delay errors", func(t *testing.T) {
		_, err := (Mapping{
			Request: MappingRequest{Method: "GET", Path: "/x"},
			Response: &MappingResponse{
				Status:   200,
				BodyFile: "missing-file.json",
			},
		}).ToExpectation()
		if err == nil {
			t.Fatal("expected missing body file error")
		}

		_, err = (Mapping{
			Request: MappingRequest{Method: "GET", Path: "/x"},
			Response: &MappingResponse{
				Status: 200,
				Delay:  "not-a-duration",
			},
		}).ToExpectation()
		if err == nil {
			t.Fatal("expected invalid duration error")
		}
	})
}

func TestPublicHelpersAndTemplateFailures(t *testing.T) {
	ms := NewMockServer()
	defer ms.Close()

	exp := NewExpectation().
		WithRequestMethod("GET").
		WithPath("/templated/{id}").
		WithPathVariables(map[string]string{"id": "7"}).
		WithQueryParamMatching("trace", "^t-[0-9]+$").
		WithHeaderMatching("X-Mode", "^prod|test$").
		WithCookieMatching("session", "^s[0-9]+$").
		WithPriority(1).
		WithResponseHeaderTemplate("X-ID", `{{index .PathVariables "id"}}`).
		AndRespondWithTemplate(`{"id":"{{index .PathVariables "id"}}","trace":"{{first (index .Query "trace")}}"}`, 200)
	ms.AddExpectation(exp)

	req, err := http.NewRequest(http.MethodGet, ms.URL()+"/templated/7?trace=t-123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Mode", "prod")
	req.AddCookie(&http.Cookie{Name: "session", Value: "s9"})

	resp, err := ms.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.Header.Get("X-ID") != "7" || !strings.Contains(string(body), `"trace":"t-123"`) {
		t.Fatalf("unexpected templated response: status=%d header=%q body=%s", resp.StatusCode, resp.Header.Get("X-ID"), string(body))
	}

	all := ms.AllRequests()
	if len(all) != 1 {
		t.Fatalf("expected one request in AllRequests, got %d", len(all))
	}
	matched := true
	found := ms.FindRequests(RequestFilter{Method: http.MethodGet, Path: "/templated/7", Matched: &matched})
	if len(found) != 1 {
		t.Fatalf("expected FindRequests match, got %d", len(found))
	}
	ms.ClearRequests()
	if len(ms.GetRequests()) != 0 || len(ms.GetUnmatchedRequests()) != 0 {
		t.Fatal("expected ClearRequests to empty journal")
	}

	t.Run("function responder", func(t *testing.T) {
		funcServer := NewMockServer()
		defer funcServer.Close()

		funcServer.AddExpectation(NewExpectation().
			WithRequestMethod("POST").
			WithPath("/func").
			AndRespondWithFunc(func(w http.ResponseWriter, r *http.Request, body []byte) {
				w.Header().Set("X-Body", string(body))
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte("handled"))
			}))

		resp, err := http.Post(funcServer.URL()+"/func", "text/plain", strings.NewReader("payload"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted || resp.Header.Get("X-Body") != "payload" || string(body) != "handled" {
			t.Fatalf("unexpected function response: %d %q %s", resp.StatusCode, resp.Header.Get("X-Body"), string(body))
		}
	})

	t.Run("template render failures", func(t *testing.T) {
		badTemplateServer := NewMockServer()
		defer badTemplateServer.Close()
		badTemplateServer.AddExpectation(NewExpectation().
			WithRequestMethod("GET").
			WithPath("/bad-template").
			AndRespondWithTemplate(`{{`, 200))

		resp, err := http.Get(badTemplateServer.URL() + "/bad-template")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected template parse failure to return 500, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()

		badHeaderServer := NewMockServer()
		defer badHeaderServer.Close()
		badHeaderServer.AddExpectation(NewExpectation().
			WithRequestMethod("GET").
			WithPath("/bad-header").
			WithResponseHeaderTemplate("X-Bad", `{{`).
			AndRespondWithString("ok", 200))

		resp, err = http.Get(badHeaderServer.URL() + "/bad-header")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Bad") != "" {
			t.Fatalf("expected bad header template to be skipped, got status=%d header=%q", resp.StatusCode, resp.Header.Get("X-Bad"))
		}
		_ = resp.Body.Close()
	})
}

func TestServerEngineHelpersAndAdminBranches(t *testing.T) {
	engine := NewMockServerEngine(&Config{
		Protocol: HTTPS,
		TLSConfig: &TLSOptions{
			RequireClientCert:  true,
			SkipClientVerify:   true,
			InsecureSkipVerify: true,
		},
	})
	if engine.URL() != "" {
		t.Fatal("expected engine URL to be empty before httptest startup")
	}
	if engine.ServerTLSConfig() == nil {
		t.Fatal("expected ServerTLSConfig")
	}
	engine.Use(func(next http.Handler) http.Handler { return next })

	t.Run("admin delete and reset", func(t *testing.T) {
		ms := NewMockServer()
		defer ms.Close()

		_, _ = http.Get(ms.URL() + "/missing")

		req, _ := http.NewRequest(http.MethodDelete, ms.URL()+"/__moxy/requests", nil)
		resp, err := ms.DefaultClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected delete requests 200, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()

		if err := ms.AddMapping(Mapping{
			Request:  MappingRequest{Method: "GET", Path: "/x"},
			Response: &MappingResponse{Status: 200, Body: "ok"},
		}); err != nil {
			t.Fatal(err)
		}

		req, _ = http.NewRequest(http.MethodDelete, ms.URL()+"/__moxy/mappings", nil)
		resp, err = ms.DefaultClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected delete mappings 200, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()

		req, _ = http.NewRequest(http.MethodPost, ms.URL()+"/__moxy/reset", nil)
		resp, err = ms.DefaultClient().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected reset 200, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()

		resp, err = http.Get(ms.URL() + "/__moxy/unknown")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected unknown admin endpoint 404, got %d", resp.StatusCode)
		}
		_ = resp.Body.Close()
	})

	t.Run("error string and remove expectation false", func(t *testing.T) {
		errObj := &ExpectationError{Message: "boom", Details: []string{"a", "b"}}
		msg := errObj.Error()
		if !strings.Contains(msg, "boom") || !strings.Contains(msg, "a") || !strings.Contains(msg, "b") {
			t.Fatalf("unexpected error string: %s", msg)
		}

		ms := NewMockServer()
		defer ms.Close()
		if ms.RemoveExpectation(NewExpectation()) {
			t.Fatal("expected remove to return false for unknown expectation")
		}
	})
}

func TestAssertionFailureAndRequestHelpers(t *testing.T) {
	e := NewExpectation().WithRequestMethod("GET").WithPath("/x")
	if err := e.AssertCalled(1); err == nil {
		t.Fatal("expected AssertCalled failure")
	}

	req := httptest.NewRequest(http.MethodGet, "/simple", nil)
	if got := capturePathVariables(e, req.URL.Path); len(got) != 0 {
		t.Fatalf("expected no path captures, got %+v", got)
	}
}

func TestRenderTemplateHelperFailurePath(t *testing.T) {
	_, err := renderTemplate(`{{index .PathVariables "id"`, templateData{})
	if err == nil {
		t.Fatal("expected template parse error")
	}

	_, ok := valueAtDotPath(errors.New("x"), "field")
	if ok {
		t.Fatal("expected valueAtDotPath failure for non-object")
	}
}
