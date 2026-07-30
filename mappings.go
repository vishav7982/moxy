package moxy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Mapping is moxy's native JSON representation for a mock endpoint.
type Mapping struct {
	Name      string            `json:"name,omitempty"`
	Priority  int               `json:"priority,omitempty"`
	Request   MappingRequest    `json:"request"`
	Response  *MappingResponse  `json:"response,omitempty"`
	Responses []MappingResponse `json:"responses,omitempty"`
	Times     *int              `json:"times,omitempty"`
}

// MappingRequest defines request matching fields in JSON mapping files.
type MappingRequest struct {
	Method        string                  `json:"method"`
	Path          string                  `json:"path,omitempty"`
	PathPattern   string                  `json:"pathPattern,omitempty"`
	Headers       map[string]string       `json:"headers,omitempty"`
	HeaderMatches map[string]ValueMatcher `json:"headerMatches,omitempty"`
	Query         map[string]string       `json:"query,omitempty"`
	QueryMatches  map[string]ValueMatcher `json:"queryMatches,omitempty"`
	Cookies       map[string]ValueMatcher `json:"cookies,omitempty"`
	BasicAuth     *BasicAuth              `json:"basicAuth,omitempty"`
	Body          string                  `json:"body,omitempty"`
	JSONBody      json.RawMessage         `json:"jsonBody,omitempty"`
	BodyContains  string                  `json:"bodyContains,omitempty"`
	JSONFields    map[string]interface{}  `json:"jsonFields,omitempty"`
}

// BasicAuth defines username/password matching for mapping files.
type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// MappingResponse defines response fields in JSON mapping files.
type MappingResponse struct {
	Status          int               `json:"status,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	HeaderTemplates map[string]string `json:"headerTemplates,omitempty"`
	Body            string            `json:"body,omitempty"`
	BodyFile        string            `json:"bodyFile,omitempty"`
	BodyTemplate    string            `json:"bodyTemplate,omitempty"`
	Delay           string            `json:"delay,omitempty"`
	Timeout         bool              `json:"timeout,omitempty"`
}

// LoadMappings loads all *.json mapping files from a directory.
func (m *MockServer) LoadMappings(path string) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("load mappings from %q: %w", path, err)
	}

	type loadedMapping struct {
		name    string
		mapping Mapping
		baseDir string
	}
	var loaded []loadedMapping
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(path, entry.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("read mapping %q: %w", entry.Name(), err)
		}
		var mapping Mapping
		if err := json.Unmarshal(data, &mapping); err != nil {
			return fmt.Errorf("parse mapping %q: %w", entry.Name(), err)
		}
		loaded = append(loaded, loadedMapping{
			name:    entry.Name(),
			mapping: mapping,
			baseDir: filepath.Dir(fullPath),
		})
	}

	sort.SliceStable(loaded, func(i, j int) bool {
		left, right := normalizedPriority(loaded[i].mapping.Priority), normalizedPriority(loaded[j].mapping.Priority)
		if left == right {
			return loaded[i].name < loaded[j].name
		}
		return left < right
	})

	for _, item := range loaded {
		if err := m.addMapping(item.mapping, item.baseDir); err != nil {
			return fmt.Errorf("add mapping %q: %w", item.name, err)
		}
	}
	return nil
}

// AddMapping converts a native JSON mapping into an expectation.
func (m *MockServer) AddMapping(mapping Mapping) error {
	return m.addMapping(mapping, "")
}

func (m *MockServer) addMapping(mapping Mapping, baseDir string) error {
	exp, err := mapping.toExpectation(baseDir)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations = append(m.expectations, exp)
	m.mappings = append(m.mappings, mapping)
	m.sortExpectationsLocked()
	return nil
}

// ToExpectation converts a mapping to the runtime expectation representation.
func (mapping Mapping) ToExpectation() (*Expectation, error) {
	return mapping.toExpectation("")
}

func (mapping Mapping) toExpectation(baseDir string) (*Expectation, error) {
	if mapping.Request.Method == "" {
		return nil, fmt.Errorf("mapping request.method is required")
	}
	if mapping.Request.Path == "" && mapping.Request.PathPattern == "" {
		return nil, fmt.Errorf("mapping request.path or request.pathPattern is required")
	}
	if mapping.Response == nil && len(mapping.Responses) == 0 {
		return nil, fmt.Errorf("mapping response or responses is required")
	}

	exp := NewExpectation().WithRequestMethod(mapping.Request.Method)
	exp.Priority = mapping.Priority
	exp.MaxCalls = mapping.Times

	if mapping.Request.PathPattern != "" {
		compiled, err := regexp.Compile(mapping.Request.PathPattern)
		if err != nil {
			return nil, fmt.Errorf("invalid request.pathPattern: %w", err)
		}
		exp.Request.PathPattern = compiled
	} else {
		exp.WithPath(mapping.Request.Path)
		exp.Request.Path = mapping.Request.Path
	}

	for key, value := range mapping.Request.Headers {
		exp.WithHeader(key, value)
	}
	if len(mapping.Request.HeaderMatches) > 0 {
		exp.Request.HeaderMatchers = make(map[string]ValueMatcher, len(mapping.Request.HeaderMatches))
		for key, matcher := range mapping.Request.HeaderMatches {
			exp.Request.HeaderMatchers[strings.ToLower(key)] = matcher
		}
	}
	for key, value := range mapping.Request.Query {
		exp.WithQueryParam(key, value)
	}
	exp.Request.QueryParamMatchers = mapping.Request.QueryMatches
	exp.Request.Cookies = mapping.Request.Cookies
	exp.Request.JSONFields = mapping.Request.JSONFields
	if mapping.Request.BasicAuth != nil {
		exp.WithBasicAuth(mapping.Request.BasicAuth.Username, mapping.Request.BasicAuth.Password)
	}
	if mapping.Request.Body != "" {
		exp.WithRequestBodyString(mapping.Request.Body)
	}
	if len(mapping.Request.JSONBody) > 0 {
		exp.WithRequestJSONBody(string(mapping.Request.JSONBody))
	}
	if mapping.Request.BodyContains != "" {
		exp.WithRequestBodyContains(mapping.Request.BodyContains)
	}

	responses := mapping.Responses
	if mapping.Response != nil {
		responses = []MappingResponse{*mapping.Response}
	}
	for i, response := range responses {
		if i > 0 {
			exp.NextResponse()
		}
		if err := applyMappingResponse(exp, response, baseDir); err != nil {
			return nil, err
		}
	}
	return exp, nil
}

func applyMappingResponse(exp *Expectation, response MappingResponse, baseDir string) error {
	resp := exp.getCurrentResponse()
	if response.Status == 0 {
		resp.StatusCode = 200
	} else {
		resp.StatusCode = response.Status
	}
	resp.Headers = response.Headers
	resp.HeaderTemplates = response.HeaderTemplates
	resp.Body = []byte(response.Body)
	resp.BodyTemplate = response.BodyTemplate
	resp.TimeoutSimulation = response.Timeout

	if response.BodyFile != "" {
		bodyFile, err := resolveMappingBodyFile(response.BodyFile, baseDir)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return fmt.Errorf("read response.bodyFile %q: %w", response.BodyFile, err)
		}
		resp.Body = data
	}
	if response.Delay != "" {
		delay, err := time.ParseDuration(response.Delay)
		if err != nil {
			return fmt.Errorf("invalid response.delay %q: %w", response.Delay, err)
		}
		resp.Delay = delay
	}
	return nil
}

func resolveMappingBodyFile(bodyFile, baseDir string) (string, error) {
	if baseDir == "" {
		return bodyFile, nil
	}

	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve mapping directory %q: %w", baseDir, err)
	}
	candidate := bodyFile
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	candidate, err = filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", fmt.Errorf("resolve response.bodyFile %q: %w", bodyFile, err)
	}
	rel, err := filepath.Rel(base, candidate)
	if err != nil {
		return "", fmt.Errorf("resolve response.bodyFile %q: %w", bodyFile, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("response.bodyFile %q escapes mapping directory", bodyFile)
	}
	return candidate, nil
}

func (m *MockServer) sortExpectationsLocked() {
	sort.SliceStable(m.expectations, func(i, j int) bool {
		left, right := normalizedPriority(m.expectations[i].Priority), normalizedPriority(m.expectations[j].Priority)
		return left < right
	})
}

func normalizedPriority(priority int) int {
	if priority == 0 {
		return 5
	}
	return priority
}
