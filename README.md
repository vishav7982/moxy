# moxy

A blazing-fast, zero-config **HTTP/HTTPS** & **mTLS** mock server for Go — built on **httptest.Server**, designed for realistic, reproducible, and secure integration tests.
Stop fighting with Docker containers, flaky network calls, and manual certificate generation. With **moxy**, you can spin up a fully functional mock server (HTTP or HTTPS) in seconds, define expectations with a clean DSL, and test client behavior under real-world scenarios — including mutual TLS, sequential responses, delays, and timeouts — without leaving memory.

✅ Perfect for **CI/CD** pipelines, **retry logic** testing, **OAuth** flows, and **secure service-to-service** communication tests

![Go CI](https://github.com/vishav7982/moxy/actions/workflows/moxy-ci.yml/badge.svg?branch=main)
[![Coverage](https://codecov.io/gh/vishav7982/moxy/branch/main/graph/badge.svg)](https://codecov.io/gh/vishav7982/moxy)
---

## Features
- Flexible request matching: HTTP method, path, headers, query params, and body.
- Support for path variables, regular expressions, cookies, basic auth, and simple JSON field matching.
- Multiple response types: string, file, Go template, or custom function.
- Sequential responses for repeated calls.
- Simulate delays or timeouts.
- Standalone `moxy` binary with JSON mapping files.
- Runtime admin API under `/__moxy`.
- Request journal for matched and unmatched requests.
- MaxCalls enforcement and unmatched request tracking.
- Thread-safe with call count tracking.
- Middleware support and verbose logging.
- HTTPS support with self-signed or custom certificates.
- Mutual TLS (mTLS) support for client certificate verification.
---

## Install

```bash
go get github.com/vishav7982/moxy
```

Install the standalone binary:

```bash
go install github.com/vishav7982/moxy/cmd/moxy@latest
```

## Quick Start
```Go
ms := moxy.NewMockServer()
defer ms.Close()

exp := moxy.NewExpectation().
        WithRequestMethod("GET").
        WithPath("/ping").
        AndRespondWithString(`{"message":"pong"}`, 200)
ms.AddExpectation(exp)

resp, _ := http.Get(ms.URL() + "/ping")
body, _ := io.ReadAll(resp.Body)

fmt.Println(string(body)) // {"message":"pong"}
```

## Standalone Usage

Run `moxy` as a local mock server and load JSON mappings from a directory:

```bash
moxy --host 127.0.0.1 --port 8080 --mappings ./mappings --verbose
```

Example mapping:

```json
{
  "name": "get-user",
  "request": {
    "method": "GET",
    "path": "/users/{id}"
  },
  "response": {
    "status": 200,
    "headers": {
      "Content-Type": "application/json"
    },
    "bodyTemplate": "{\"id\":\"{{index .PathVariables \"id\"}}\"}"
  }
}
```

Runtime admin endpoints are available under `/__moxy`:

- `GET /__moxy/health`
- `GET|POST|DELETE /__moxy/mappings`
- `GET|DELETE /__moxy/requests`
- `POST /__moxy/reset`

📖 For more extensive usage examples — including https, mTLS, headers, query parameters, sequential responses, response delays, simulated server timeouts, custom responders, unmatched request handling etc. — see [mock_server_test.go](./mock_server_test.go).
## Why Use It ?
Modern Go projects need reliable integration tests — but setting up real HTTP(S) servers, TLS, and mTLS is painful and slow. This library solves that by giving you an in-memory, production-like HTTP/HTTPS server that is:

**✅ 1. Zero-Config HTTPS & mTLS**

Automatically generates self-signed certs for you. Supports mutual TLS (mTLS) out of the box — no need to write OpenSSL scripts or manage temp cert files manually. Lets you easily test trusted vs. untrusted client behavior in the same test suite.

**✅ 2. Fast, In-Memory, No External Dependencies**

No need to spin up Docker containers or mock services manually. No network flakiness — runs entirely in-memory, so tests are deterministic and blazing fast.

**✅ 3. Rich Expectation DSL**

Define request matchers with method, path, headers, query params, and body content. Supports multiple expectations for different endpoints.
Supports sequential responses for the same request (great for retry and polling tests).
Supports richer matching for cookies, basic auth, regex values, and JSON fields.

**✅ 4. Customizable Client & TLS Behavior**

Easily create preconfigured http.Clients that trust your mock server. Can toggle between strict verification and InsecureSkipVerify for quick-and-dirty testing.

**✅ 5. Safe, Concurrency-Friendly**

Designed for parallel tests — no global state, no race conditions. Thread-safe expectation matching and request recording.

**✅ 6. Clear Failure Reporting**

When expectations don’t match, you get detailed logs showing the unexpected request and which expectation failed. Makes debugging test failures much faster.
You can also inspect the request journal to see matched and unmatched requests.

**✅ 7. Minimal Boilerplate**

A few lines of code start a server, add expectations, and return responses. No need to manage ports manually — it binds to a free port automatically.

**✅ 8. Supports Realistic Workflows**

Perfect for testing OAuth flows, login endpoints, webhook receivers, or any HTTPS integration.

## ❓ Frequently Asked Questions

**1. Can I use this mock server for both HTTP and HTTPS?**

Yes, you can configure the protocol by passing Config{Protocol: HTTPS/HTTP} when creating the server. Default is HTTP. If you don’t provide TLS certificates, a self-signed certificate will be generated automatically.

**2. Can I define multiple expectations for different paths?**

Absolutely.
You can add multiple expectations before making requests:
```go
server.AddExpectation(NewExpectation().
WithRequestMethod("GET").
WithPath("/ping").
AndRespondWithString("pong", 200))

server.AddExpectation(NewExpectation().
WithRequestMethod("POST").
WithPath("/login").
AndRespondWithString("ok", 200))
```

**3. Does it support sequential responses for the same endpoint?**

Yes!
You can use .NextResponse() to define multiple sequential responses for the same request:
```go
e := NewExpectation().
WithRequestMethod("GET").
WithPath("/status").
AndRespondWithString("step 1", 200).
NextResponse().
AndRespondWithString("step 2", 200)

server.AddExpectation(e)

// 1st call → "step 1"
// 2nd call → "step 2"
```

Perfect for testing polling or retry behavior.

**4. How do unmatched requests behave?**

By default, unmatched requests are logged and return HTTP 418 Unmatched Request.
You can override this behavior using Config.UnmatchedStatusCode and Config.UnmatchedStatusMessage.

**5, Can I modify server behavior?**
 
Absolutely! moxy exposes a rich **Config** struct that lets you customize the server at creation time — including protocol (HTTP/HTTPS), TLS settings, logging, and even the default behavior for unmatched requests.

Example using custom HTTPS + mTLS:

```go
cert, _ := tls.LoadX509KeyPair("server.crt", "server.key")
clientCAs := x509.NewCertPool()
// Add your CA to the pool
clientCAs.AppendCertsFromPEM(caCertPEM)

cfg := mockhttpserver.Config{
Protocol: mockhttpserver.HTTPS,
TLSConfig: &mockhttpserver.TLSOptions{
Certificates:      []tls.Certificate{cert},
RequireClientCert: true,
ClientCAs:         clientCAs,
MinVersion:        tls.VersionTLS12,
},
UnmatchedStatusCode:    404,
UnmatchedStatusMessage: "Route Not Found",
VerboseLogging:         true,
}
ms := mockhttpserver.NewMockServerWithConfig(cfg)
defer ms.Close()
```

This way, you can:
- Use your own certs or let the server auto-generate a self-signed one
- Turn on mutual TLS (RequireClientCert)
- Control logging and unmatched request responses 
- Easily toggle between HTTP and HTTPS

## Usage

See [USAGE.md](./USAGE.md) for a complete guide on using **moxy**, including:
- Mocking HTTP/HTTPS requests
- Simulating timeouts and delays
- Sequential responses and advanced expectations etc.

## Contributing

Contributions are welcome! 🎉 See [CONTRIBUTION.md](./CONTRIBUTING.md) for more details.





