## Usage Guide

This document shows you how to use moxy in a realistic testing workflow. We'll walk through a simple function that makes an HTTP request, mock its dependency with moxy, and verify the behavior. Then we’ll cover HTTPS, mTLS, and advanced features like sequential responses, delays, timeouts etc.

**1. The Function Under Test – GetUser()**

Let’s say we have a function that fetches a user from a remote REST API:
```go
package sample

import (
"encoding/json"
"fmt"
"net/http"
)

type User struct {
ID   int    `json:"id"`
Name string `json:"name"`
}

// GetUser fetches a user by ID from a remote API
func GetUser(client *http.Client, baseURL string, userID int) (*User, error) {
	url := fmt.Sprintf("%s/users/%d", baseURL, userID)
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, err
	}
	return &user, nil
}
```
**Our goal**: test GetUser() without hitting a real API, by mocking /users/{id}.

**2. Setting Up moxy for HTTP**
```go
import (
    "encoding/json"
    "testing"

    "github.com/vishav7982/moxy"
)

func TestGetUser_HTTP(t *testing.T) {
    // 1. Start mock server
    ms := moxy.NewMockServer()
    defer ms.Close()

    // 2. Prepare mock data
    mockUser := User{ID: 42, Name: "Alice"}
    mockJSON, _ := json.Marshal(mockUser)

    // 3. Add expectation to match the request and respond
    exp := moxy.NewExpectation().
        WithRequestMethod("GET").
        WithPath("/users/42").
        AndRespondWithString(string(mockJSON), 200)

    ms.AddExpectation(exp)

    // 4. Call the function under test using mock server URL
    got, err := GetUser(ms.DefaultClient(),ms.URL(), 42)
    if err != nil {
        t.Fatalf("GetUser failed: %v", err)
    }

    // 5. Assert results
    if got.ID != 42 || got.Name != "Alice" {
        t.Errorf("unexpected user: %+v", got)
    }

    // 6. Verify expectation was hit
    if ms.ExpectationCallCount(exp) != 1 {
        t.Errorf("expected endpoint to be called once")
    }
}
``` 
This gives you **end-to-end** control: your code thinks it’s talking to a **real server**, but you have full control of the responses.

**3. Switching to HTTPS**

Testing clients that use TLS is easy — just start moxy in **HTTPS** mode. It generates a self-signed certificate for you and provides a preconfigured **http.Client** that trusts it.
```go
func TestGetUser_HTTPS(t *testing.T) {
ms := moxy.NewMockServerWithConfig(&moxy.Config{Protocol: moxy.HTTPS})
defer ms.Close()

exp := moxy.NewExpectation().
WithRequestMethod("GET").
WithPath("/users/42").
AndRespondWithString(`{"id":42,"name":"Alice"}`, 200)

ms.AddExpectation(exp)

// Inject mock server's trusted client into GetUser
client := ms.Client()

got, err := GetUser(client, ms.URL(), 42)
if err != nil {
t.Fatalf("HTTPS call failed: %v", err)
}

if got.ID != 42 || got.Name != "Alice" {
t.Errorf("unexpected user: %+v", got)
}

// Optional: verify expectation was hit
if ms.ExpectationCallCount(exp) != 1 {
t.Errorf("expected endpoint to be called once")
}
}
```
**4. Testing with mTLS (Mutual TLS)**

If your service requires a client certificate, configure moxy with **TLSOptions.RequireClientCert** = **true**.
```go
func TestGetUser_mTLS_HTTPS(t *testing.T) {
serverCrtFilePath := filepath.Join("testdata", "server.crt")
	serverKeyFilePath := filepath.Join("testdata", "server.key")
	serverCert, err := tls.LoadX509KeyPair(serverCrtFilePath, serverKeyFilePath)
	if err != nil {
		t.Fatalf("failed to load server cert/key: %v", err)
	}

	// Load client certificate + key
	clientCrtFilePath := filepath.Join("testdata", "client.crt")
	clientKeyFilePath := filepath.Join("testdata", "client.key")
	clientCert, err := tls.LoadX509KeyPair(clientCrtFilePath, clientKeyFilePath)
	if err != nil {
		t.Fatalf("failed to load client cert/key: %v", err)
	}

	// Create server trust pool (trust client cert)
	clientCertData, err := os.ReadFile(clientCrtFilePath)
	if err != nil {
		t.Fatalf("failed to read client cert: %v", err)
	}
	clientCertPool := x509.NewCertPool()
	if ok := clientCertPool.AppendCertsFromPEM(clientCertData); !ok {
		t.Fatal("failed to append client cert to server trust pool")
	}

	// Create client trust pool (trust server cert)
	serverCertData, err := os.ReadFile(serverCrtFilePath)
	if err != nil {
		t.Fatalf("failed to read server cert: %v", err)
	}
	serverCertPool := x509.NewCertPool()
	if ok := serverCertPool.AppendCertsFromPEM(serverCertData); !ok {
		t.Fatal("failed to append server cert to client trust pool")
	}
	// Configure mock server to require client cert (mTLS)
	server := NewMockServerWithConfig(&Config{
		Protocol: HTTPS,
		TLSConfig: &TLSOptions{
			Certificates:      []tls.Certificate{serverCert},
			RequireClientCert: true,
			ClientCAs:         clientCertPool,
		},
	})
	defer server.Close()

	// Add expectation for GET /secure
	server.AddExpectation(moxy.NewExpectation().
        WithRequestMethod("GET").
        WithPath("/users/42").
        AndRespondWithString(`{"id":42,"name":"Alice"}`, 200)
	)
	// Make request
	resp, err := GetUser(ms.MTLSClient([]tls.Certificate{clientCert}, serverCertPool), ms.URL(), 42)
    if err != nil {
     t.Fatalf("HTTPS call failed: %v", err)
    }
    if got.ID != 42 || got.Name != "Alice" { 
		t.Errorf("unexpected user: %+v", got)
	}
	// Optional: verify expectation was hit 
	if ms.ExpectationCallCount(exp) != 1 {
		t.Errorf("expected endpoint to be called once")
	}
}
```
This setup is perfect for testing services that enforce **mutual authentication** in CI.

**5. Verifying Expectations**

You can always check:
- Call count for each expectation:
  ```go
    ms.ExpectationCallCount(exp)
  ```
- All requests received for debugging:
    ```go
     for _, req := range ms.AllRequests() {
        t.Logf("Received %s %s", req.Method, req.Path)
     }
    ```

**Standalone binary with JSON mappings**

```bash
moxy --host 127.0.0.1 --port 8080 --mappings ./mappings
```

Example `mappings/user.json`:

```json
{
  "request": {
    "method": "GET",
    "path": "/users/{id}"
  },
  "response": {
    "status": 200,
    "headers": { "Content-Type": "application/json" },
    "bodyTemplate": "{\"id\":\"{{index .PathVariables \"id\"}}\"}"
  }
}
```
  
This ensures your code made the right number of HTTP calls with the right data.

## 🔧 Advanced Usage
**Sequential Responses**

Great for testing polling or retry logic:
```go
exp := moxy.NewExpectation().
WithPath("/status").
AndRespondWithString("pending", 202).
NextResponse().
AndRespondWithString("processing", 202).
NextResponse().
AndRespondWithString("done", 200)

ms.AddExpectation(exp)
```
Each call to **/status** will return the next response in sequence.

**Adding Delays**

Simulate slow endpoints:
```go
exp := moxy.NewExpectation().
WithPath("/slow").
WithResponseDelay(2 * time.Second).
AndRespondWithString("finally", 200)
```

**Simulating Timeouts**

You can simulate a timeout using **SimulateTimeout()** on an expectation. This is useful for testing retry logic or client-side timeout handling.
```go
ms := moxy.NewMockServer()
defer ms.Close()

// Expectation that simulates a timeout
ms.AddExpectation(
moxy.NewExpectation().
WithRequestMethod("GET").
WithPath("/slow-endpoint").
SimulateTimeout(),
)
// Client with a 100ms timeout
client := &http.Client{Timeout: 100 * time.Millisecond}
start := time.Now()
_, err := client.Get(ms.URL() + "/slow-endpoint")
elapsed := time.Since(start)
if err == nil {
t.Fatalf("expected timeout error, got none")
}
if !strings.Contains(err.Error(), "Client.Timeout") {
t.Errorf("expected timeout error, got: %v", err)
}
if elapsed < 100*time.Millisecond {
t.Errorf("request returned too quickly, took only %v", elapsed)
}
```
**Key Takeaways:**
- **SimulateTimeout**() causes the server to hold the request open **until the client gives up**.
- You control how quickly the test fails by setting **http.Client.Timeout.**
- Perfect for verifying **retry mechanisms and graceful error handling** in your code.
## 📚 More Usage Examples

The [expectations_test.go](./expectations_test.go) and [mock_server_test.go](./mock_server_test.go) files in this repository contain additional real-world examples of using **moxy**.

They cover scenarios such as:
- Matching requests by method, path, headers, and query parameters.
- Returning dynamic responses using functions instead of static strings.
- Testing HTTPS setups and verifying TLS handshake works.
- Simulating error responses (e.g., 404/500) to test retry logic or error handling.
- Sequential responses to validate behavior over multiple calls.
- Verifying expectation call counts to ensure your code made the right number of HTTP requests.

You can explore these tests to see advanced usage patterns and take reference from snippets there into your own test suites.
