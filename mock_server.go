package moxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"text/template"
	"time"
)

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Protocol:               HTTP,
		UnmatchedStatusCode:    http.StatusTeapot,
		UnmatchedStatusMessage: "Unmatched Request",
		LogUnmatched:           true,
		MaxBodySize:            10 << 20, // 10MB
		VerboseLogging:         false,
	}
}

// mergeWithDefaults fills missing fields in custom config with defaults.
func mergeWithDefaults(custom *Config) *Config {
	if custom == nil {
		return DefaultConfig()
	}
	def := DefaultConfig()
	if custom.UnmatchedStatusCode == 0 {
		custom.UnmatchedStatusCode = def.UnmatchedStatusCode
	}
	if custom.UnmatchedStatusMessage == "" {
		custom.UnmatchedStatusMessage = def.UnmatchedStatusMessage
	}
	if custom.MaxBodySize == 0 {
		custom.MaxBodySize = def.MaxBodySize
	}
	return custom
}

// NewMockServer initializes a new MockServer with default configuration and logger.
func NewMockServer() *MockServer {
	return NewMockServerWithConfig(DefaultConfig())
}

// NewMockServerWithConfig initializes a new MockServer with custom configuration.
func NewMockServerWithConfig(customConfig *Config) *MockServer {
	ms := NewMockServerEngine(customConfig)
	config := &ms.config

	if config.Protocol == HTTPS {
		server := httptest.NewUnstartedServer(ms.Handler())
		server.TLS = buildTLSConfig(config.TLSConfig)
		server.StartTLS()
		ms.server = server
	} else {
		ms.server = httptest.NewServer(ms.Handler())
	}
	return ms
}

// NewMockServerEngine creates a MockServer without starting an httptest.Server.
// This is useful for standalone binary mode where the caller owns the listener.
func NewMockServerEngine(customConfig *Config) *MockServer {
	config := mergeWithDefaults(customConfig)
	return &MockServer{
		logger: log.New(os.Stdout, "[MockServer] ", log.LstdFlags|log.Lshortfile),
		config: *config,
	}
}

// ServerTLSConfig returns the server TLS config for standalone HTTPS mode.
func (m *MockServer) ServerTLSConfig() *tls.Config {
	return buildTLSConfig(m.config.TLSConfig)
}

// buildTLSConfig builds a *tls.Config from TLSOptions.
func buildTLSConfig(opts *TLSOptions) *tls.Config {
	tlsConfig := &tls.Config{}

	if opts == nil {
		tlsConfig.Certificates = []tls.Certificate{generateDefaultCert()}
		tlsConfig.InsecureSkipVerify = true
		return tlsConfig
	}
	if opts.MinVersion != 0 {
		tlsConfig.MinVersion = opts.MinVersion
	} else {
		tlsConfig.MinVersion = tls.VersionTLS12 // default
	}
	// Server certs
	if len(opts.Certificates) > 0 {
		tlsConfig.Certificates = opts.Certificates
	} else {
		tlsConfig.Certificates = []tls.Certificate{generateDefaultCert()}
	}
	// mTLS configuration
	if opts.RequireClientCert {
		if opts.SkipClientVerify {
			tlsConfig.ClientAuth = tls.RequireAnyClientCert
		} else {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			tlsConfig.ClientCAs = opts.ClientCAs
		}
	}
	// Allow skipping verification (self-signed)
	tlsConfig.InsecureSkipVerify = opts.InsecureSkipVerify
	return tlsConfig
}

// WithLogger allows injecting a custom logger.
func (m *MockServer) WithLogger(logger *log.Logger) *MockServer {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
	return m
}

// WithUnmatchedResponder allows setting a custom handler for unmatched requests.
func (m *MockServer) WithUnmatchedResponder(
	handler func(w http.ResponseWriter, r *http.Request, req UnmatchedRequest),
) *MockServer {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unmatchedResponder = handler
	return m
}

// Close shuts down the mock server.
func (m *MockServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

// URL returns the base URL of the mock server.
func (m *MockServer) URL() string {
	if m.server == nil {
		return ""
	}
	return m.server.URL
}

// AddExpectation registers an expectation against which requests are matched.
func (m *MockServer) AddExpectation(e *Expectation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations = append(m.expectations, e)
	m.sortExpectationsLocked()
}

// ClearExpectations removes all registered expectations.
func (m *MockServer) ClearExpectations() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expectations = m.expectations[:0]
	m.mappings = m.mappings[:0]
}

// RemoveExpectation removes a specific expectation. Returns true if found and removed.
func (m *MockServer) RemoveExpectation(e *Expectation) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, exp := range m.expectations {
		if exp == e {
			m.expectations = append(m.expectations[:i], m.expectations[i+1:]...)
			return true
		}
	}
	return false
}

// Handler returns the HTTP handler used by both httptest and standalone modes.
func (m *MockServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__moxy/", m.adminHandler)
	mux.HandleFunc("/", m.handler)
	return mux
}

// GetUnmatchedRequests returns a copy of all unmatched requests.
func (m *MockServer) GetUnmatchedRequests() []UnmatchedRequest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]UnmatchedRequest, len(m.unmatchedRequests))
	copy(result, m.unmatchedRequests)
	return result
}

// GetRequests returns a copy of all received requests.
func (m *MockServer) GetRequests() []RequestRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]RequestRecord, len(m.requests))
	copy(result, m.requests)
	return result
}

// AllRequests is an alias for GetRequests.
func (m *MockServer) AllRequests() []RequestRecord {
	return m.GetRequests()
}

// FindRequests returns requests matching the supplied filter.
func (m *MockServer) FindRequests(filter RequestFilter) []RequestRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []RequestRecord
	for _, req := range m.requests {
		if filter.Method != "" && req.Method != filter.Method {
			continue
		}
		if filter.Path != "" && req.Path != filter.Path {
			continue
		}
		if filter.Matched != nil && req.Matched != *filter.Matched {
			continue
		}
		result = append(result, req)
	}
	return result
}

// ClearUnmatchedRequests clears the history of unmatched requests.
func (m *MockServer) ClearUnmatchedRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unmatchedRequests = m.unmatchedRequests[:0]
}

// ClearRequests clears the full request journal and unmatched request history.
func (m *MockServer) ClearRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = m.requests[:0]
	m.unmatchedRequests = m.unmatchedRequests[:0]
}

// VerifyExpectations checks if all expectations were called the expected number of times.
func (m *MockServer) VerifyExpectations() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var unmet []string
	for _, exp := range m.expectations {
		if exp.MaxCalls != nil && exp.InvocationCount != *exp.MaxCalls {
			unmet = append(unmet, exp.String())
		}
	}

	if len(unmet) > 0 {
		return &ExpectationError{
			Message: "Unmet expectations found",
			Details: unmet,
		}
	}
	return nil
}

// handler processes incoming HTTP requests and returns the configured mock response.
func (m *MockServer) handler(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var err error
	if r.Body != nil {
		if m.config.MaxBodySize > 0 {
			r.Body = http.MaxBytesReader(w, r.Body, m.config.MaxBodySize)
		}
		body, err = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			m.logger.Printf("Failed to read request body: %v", err)
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
	}
	if m.config.VerboseLogging {
		m.logger.Printf("Incoming request: %s %s, Headers: %+v, Body: %s",
			r.Method, r.URL.String(), r.Header, string(body))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := requestRecordFrom(r, body)
	for _, exp := range m.expectations {
		if exp.matches(r, body) {
			if exp.MaxCalls != nil && exp.InvocationCount >= *exp.MaxCalls {
				continue
			}
			record.Matched = true
			m.requests = append(m.requests, record)
			exp.InvocationCount++
			resp := ResponseDefinition{}
			// If user configured responses, pick the right one
			if len(exp.Responses) > 0 {
				resp = exp.Responses[exp.NextResponseIndex]
				if exp.NextResponseIndex < len(exp.Responses)-1 {
					exp.NextResponseIndex++
				}
			}
			if resp.TimeoutSimulation {
				<-r.Context().Done() // blocks until the request is canceled by the client
				return
			}
			// Simulate delayed response.
			if resp.Delay > 0 {
				time.Sleep(resp.Delay)
			}
			if resp.Responder != nil {
				resp.Responder(w, r, body)
				return
			}
			templateData := newTemplateData(r, body, exp)
			renderedHeaders := renderHeaderTemplates(resp, templateData, m.logger)
			for key, value := range resp.Headers {
				w.Header().Set(key, value)
			}
			for key, value := range renderedHeaders {
				w.Header().Set(key, value)
			}
			responseBody := resp.Body
			if resp.BodyTemplate != "" {
				renderedBody, err := renderTemplate(resp.BodyTemplate, templateData)
				if err != nil {
					m.logger.Printf("Failed to render response template: %v", err)
					http.Error(w, "failed to render response template", http.StatusInternalServerError)
					return
				}
				responseBody = []byte(renderedBody)
			}
			w.WriteHeader(resp.StatusCode)
			if _, err := w.Write(responseBody); err != nil {
				m.logger.Printf("Failed to write response: %v", err)
			}
			if m.config.VerboseLogging {
				m.logger.Printf("Matched expectation, responding with status %d", resp.StatusCode)
			}
			return
		}
	}

	// No match -> record unmatched
	m.requests = append(m.requests, record)
	unmatched := UnmatchedRequest{
		Method:    r.Method,
		URL:       r.URL.RequestURI(),
		Headers:   map[string][]string(r.Header),
		Body:      string(body),
		Timestamp: time.Now(),
	}
	m.unmatchedRequests = append(m.unmatchedRequests, unmatched)

	if m.config.LogUnmatched {
		m.logger.Printf("Unexpected Request:\nMethod=%s\nURI=%s\nHeaders=%+v\nBody=%s\n",
			r.Method, r.URL.RequestURI(), r.Header, string(body))
	}

	if m.unmatchedResponder != nil {
		m.unmatchedResponder(w, r, unmatched)
		return
	}
	_ = fmt.Sprintf("Unmatched Request:\nMethod=%s\nURI=%s\nHeaders=%+v\nBody=%s\n", r.Method, r.URL.RequestURI(), r.Header, string(body))
	http.Error(w, m.config.UnmatchedStatusMessage, m.config.UnmatchedStatusCode)
}

// DefaultClient returns a simple *http.Client for HTTP/HTTPS testing.
// This client:
//   - Works for HTTP
//   - Works for HTTPS with server certs if InsecureSkipVerify is true
//   - DOES NOT handle mTLS; for that, create a custom client with TLS config
func (m *MockServer) DefaultClient() *http.Client {
	transport := &http.Transport{}
	if m.config.Protocol == HTTPS {
		// Simple HTTPS client
		tlsConfig := &tls.Config{}
		if m.config.TLSConfig != nil {
			// Default client should always skip verification for normal HTTPS
			// (unless explicitly required otherwise)
			tlsConfig.InsecureSkipVerify = true
		} else {
			tlsConfig.InsecureSkipVerify = true
		}
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{Transport: transport}
}

// Client is an alias for DefaultClient.
func (m *MockServer) Client() *http.Client {
	return m.DefaultClient()
}

// mTLSClient returns an *http.Client configured for mutual TLS.
// It uses the TLSOptions from the server. The caller provides client certificates and RootCAs.
// This is useful for testing mTLS scenarios where the server verifies the client certificate.
func (m *MockServer) mTLSClient(clientCerts []tls.Certificate, rootCAs *x509.CertPool) *http.Client {
	return m.MTLSClient(clientCerts, rootCAs)
}

// MTLSClient returns an *http.Client configured for mutual TLS.
func (m *MockServer) MTLSClient(clientCerts []tls.Certificate, rootCAs *x509.CertPool) *http.Client {
	tlsConfig := &tls.Config{
		Certificates: clientCerts, // allow multiple client certs
		RootCAs:      rootCAs,     // trust server cert
		MinVersion:   tls.VersionTLS12,
	}

	// If the server requires client certs, ensure the client provides one
	if m.config.TLSConfig != nil && m.config.TLSConfig.RequireClientCert {
		tlsConfig.InsecureSkipVerify = false
	} else {
		tlsConfig.InsecureSkipVerify = true
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}
}

// Use adds middleware to the mock server (applied to all requests).
func (m *MockServer) Use(middleware func(http.Handler) http.Handler) {
	if m.server != nil {
		m.server.Config.Handler = middleware(m.server.Config.Handler)
	}
}

// ExpectationCallCount returns how many times an expectation matched.
func (m *MockServer) ExpectationCallCount(e *Expectation) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return e.InvocationCount
}

func (e *ExpectationError) Error() string {
	result := e.Message
	for _, detail := range e.Details {
		result += "\n  " + detail
	}
	return result
}

func requestRecordFrom(r *http.Request, body []byte) RequestRecord {
	cookies := make(map[string]string)
	for _, cookie := range r.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	return RequestRecord{
		Method:    r.Method,
		URL:       r.URL.RequestURI(),
		Path:      r.URL.Path,
		Query:     map[string][]string(r.URL.Query()),
		Headers:   map[string][]string(r.Header),
		Cookies:   cookies,
		Body:      string(body),
		Timestamp: time.Now(),
	}
}

type templateData struct {
	Method        string
	Path          string
	PathVariables map[string]string
	Query         map[string][]string
	Headers       map[string][]string
	Cookies       map[string]string
	Body          string
	JSON          interface{}
}

func newTemplateData(r *http.Request, body []byte, exp *Expectation) templateData {
	var jsonBody interface{}
	_ = json.Unmarshal(body, &jsonBody)
	data := templateData{
		Method:        r.Method,
		Path:          r.URL.Path,
		PathVariables: capturePathVariables(exp, r.URL.Path),
		Query:         map[string][]string(r.URL.Query()),
		Headers:       map[string][]string(r.Header),
		Cookies:       make(map[string]string),
		Body:          string(body),
		JSON:          jsonBody,
	}
	for _, cookie := range r.Cookies() {
		data.Cookies[cookie.Name] = cookie.Value
	}
	return data
}

func capturePathVariables(exp *Expectation, path string) map[string]string {
	result := make(map[string]string)
	if exp.Request.PathPattern == nil {
		return result
	}
	matches := exp.Request.PathPattern.FindStringSubmatch(path)
	if matches == nil {
		return result
	}
	for i, name := range exp.Request.PathPattern.SubexpNames() {
		if i > 0 && name != "" {
			result[name] = matches[i]
		}
	}
	return result
}

func renderHeaderTemplates(resp ResponseDefinition, data templateData, logger *log.Logger) map[string]string {
	result := make(map[string]string)
	for key, valueTemplate := range resp.HeaderTemplates {
		value, err := renderTemplate(valueTemplate, data)
		if err != nil {
			logger.Printf("Failed to render response header template %s: %v", key, err)
			continue
		}
		result[key] = value
	}
	return result
}

func renderTemplate(source string, data templateData) (string, error) {
	tpl, err := template.New("moxy-response").Funcs(template.FuncMap{
		"first": func(values []string) string {
			if len(values) == 0 {
				return ""
			}
			return values[0]
		},
		"jsonPath": func(path string) interface{} {
			value, _ := valueAtDotPath(data.JSON, path)
			return value
		},
	}).Parse(source)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (m *MockServer) adminHandler(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodGet && path == "/__moxy/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case r.Method == http.MethodGet && path == "/__moxy/mappings":
		m.mu.RLock()
		mappings := append([]Mapping(nil), m.mappings...)
		m.mu.RUnlock()
		writeJSON(w, http.StatusOK, mappings)
	case r.Method == http.MethodPost && path == "/__moxy/mappings":
		var mapping Mapping
		if err := json.NewDecoder(r.Body).Decode(&mapping); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := m.AddMapping(mapping); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, mapping)
	case r.Method == http.MethodDelete && path == "/__moxy/mappings":
		m.ClearExpectations()
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
	case r.Method == http.MethodGet && path == "/__moxy/requests":
		writeJSON(w, http.StatusOK, m.GetRequests())
	case r.Method == http.MethodDelete && path == "/__moxy/requests":
		m.ClearRequests()
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
	case r.Method == http.MethodPost && path == "/__moxy/reset":
		m.ClearExpectations()
		m.ClearRequests()
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown moxy admin endpoint"})
	}
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
