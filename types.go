package moxy

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"time"
)

// Protocol type
type Protocol string

const (
	HTTP  Protocol = "http"
	HTTPS Protocol = "https"
)

// ResponseDefinition defines a mock response for an expectation.
type ResponseDefinition struct {
	StatusCode        int
	Body              []byte
	Headers           map[string]string
	BodyTemplate      string
	HeaderTemplates   map[string]string
	Responder         func(http.ResponseWriter, *http.Request, []byte)
	Delay             time.Duration // optional delay before sending response
	TimeoutSimulation bool          // if true, server never responds
}

// ValueMatcher supports either exact or regex-based matching.
type ValueMatcher struct {
	EqualTo string `json:"equalTo,omitempty"`
	Matches string `json:"matches,omitempty"`
}

// RequestExpectation defines the expected request structure.
type RequestExpectation struct {
	Method             string
	Path               string
	PathPattern        *regexp.Regexp
	PathVariables      map[string]string
	Body               []byte
	BodyMatcher        func([]byte) bool
	QueryParams        map[string]string
	QueryParamMatchers map[string]ValueMatcher
	Headers            map[string]string // stored as lowercase keys for case-insensitive matching
	HeaderMatchers     map[string]ValueMatcher
	Cookies            map[string]ValueMatcher
	BasicAuthUsername  string
	BasicAuthPassword  string
	JSONFields         map[string]interface{}
	BodyFromFile       bool
}

// Expectation defines a mock expectation for HTTP requests.
// It contains the expected request and one or more sequential responses.
type Expectation struct {
	Request             RequestExpectation
	Responses           []ResponseDefinition
	CreateResponseIndex int
	InvocationCount     int
	MaxCalls            *int // nil means unlimited
	NextResponseIndex   int  // tracks which response to return next
	Priority            int
}

// MockServer represents a lightweight HTTP mock server for testing HTTP clients.
type MockServer struct {
	server             *httptest.Server
	expectations       []*Expectation
	mappings           []Mapping
	requests           []RequestRecord
	unmatchedRequests  []UnmatchedRequest
	mu                 sync.RWMutex
	logger             *log.Logger
	config             Config
	unmatchedResponder func(w http.ResponseWriter, r *http.Request, req UnmatchedRequest)
}

// RequestRecord represents any request received by the mock server.
type RequestRecord struct {
	Method    string              `json:"method"`
	URL       string              `json:"url"`
	Path      string              `json:"path"`
	Query     map[string][]string `json:"query"`
	Headers   map[string][]string `json:"headers"`
	Cookies   map[string]string   `json:"cookies"`
	Body      string              `json:"body"`
	Matched   bool                `json:"matched"`
	Timestamp time.Time           `json:"timestamp"`
}

// RequestFilter is used to find recorded requests.
type RequestFilter struct {
	Method  string
	Path    string
	Matched *bool
}

// UnmatchedRequest represents a request that didn't match any expectations
type UnmatchedRequest struct {
	Method    string
	URL       string
	Headers   map[string][]string
	Body      string
	Timestamp time.Time
}

// Config holds configuration options for MockServer
type Config struct {
	Protocol               Protocol    // HTTP or HTTPS
	TLSConfig              *TLSOptions // Server's custom TLS config
	Host                   string      // Host for standalone binary mode
	Port                   int         // Port for standalone binary mode
	UnmatchedStatusCode    int         // Status code for unmatched requests (default: 418)
	UnmatchedStatusMessage string      // Status message for unmatched requests (default: "Unmatched Request")
	LogUnmatched           bool        // Whether to log unmatched requests (default: true)
	MaxBodySize            int64       // Maximum request body size in bytes (default: 10MB)
	VerboseLogging         bool        // Enable verbose request/response logging (default: false)
}

// ExpectationError represents errors related to unmet expectations
type ExpectationError struct {
	Message string
	Details []string
}

// TLSOptions for our mock https server
type TLSOptions struct {
	// Server certificate & key (if nil, a self-signed cert is generated)
	Certificates []tls.Certificate
	// Require clients to present valid certificate
	RequireClientCert bool
	// Custom RootCAs for verifying client certs (if nil, system pool is used)
	ClientCAs *x509.CertPool
	// Skip verification of client certificates (for tests)
	SkipClientVerify bool
	// Skip server certificate verification on the client side (self-signed support)
	InsecureSkipVerify bool
	// e.g., tls.VersionTLS12
	MinVersion uint16
}
