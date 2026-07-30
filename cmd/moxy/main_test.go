package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vishav7982/moxy"
)

func TestRunBuildsHTTPServer(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	var gotAddr string
	var gotHTTPS bool
	var gotMock *moxy.MockServer
	err := run([]string{"--host", "0.0.0.0", "--port", "19090", "--verbose"}, logger, func(server *http.Server, mock *moxy.MockServer, https bool) error {
		gotAddr = server.Addr
		gotHTTPS = https
		gotMock = mock
		return nil
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if gotAddr != "0.0.0.0:19090" {
		t.Fatalf("expected addr 0.0.0.0:19090, got %q", gotAddr)
	}
	if gotHTTPS {
		t.Fatal("expected http mode")
	}
	if gotMock == nil {
		t.Fatal("expected mock server engine")
	}
	if !strings.Contains(buf.String(), "moxy listening on http://0.0.0.0:19090") {
		t.Fatalf("expected log line, got %q", buf.String())
	}
}

func TestRunBuildsHTTPSServerAndLoadsMappings(t *testing.T) {
	dir := t.TempDir()
	mappingPath := filepath.Join(dir, "hello.json")
	if err := os.WriteFile(mappingPath, []byte(`{
		"request": {"method": "GET", "path": "/hello"},
		"response": {"status": 200, "body": "world"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)

	var gotHTTPS bool
	var gotMock *moxy.MockServer
	err := run([]string{"--https", "--mappings", dir}, logger, func(server *http.Server, mock *moxy.MockServer, https bool) error {
		gotHTTPS = https
		gotMock = mock
		return nil
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !gotHTTPS {
		t.Fatal("expected https mode")
	}
	if gotMock == nil {
		t.Fatal("expected mock server engine")
	}
	if len(gotMock.GetRequests()) != 0 {
		t.Fatal("expected no requests during setup")
	}
	if !strings.Contains(buf.String(), "moxy listening on https://127.0.0.1:8080") {
		t.Fatalf("expected https log line, got %q", buf.String())
	}
}

func TestRunErrors(t *testing.T) {
	logger := log.New(&bytes.Buffer{}, "", 0)

	if err := run([]string{"--bad-flag"}, logger, func(server *http.Server, mock *moxy.MockServer, https bool) error {
		return nil
	}); err == nil {
		t.Fatal("expected flag parse error")
	}

	if err := run([]string{"--mappings", filepath.Join(t.TempDir(), "missing")}, logger, func(server *http.Server, mock *moxy.MockServer, https bool) error {
		return nil
	}); err == nil || !strings.Contains(err.Error(), "failed to load mappings") {
		t.Fatalf("expected mapping load error, got %v", err)
	}

	serveErr := errors.New("boom")
	if err := run([]string{}, logger, func(server *http.Server, mock *moxy.MockServer, https bool) error {
		return serveErr
	}); !errors.Is(err, serveErr) {
		t.Fatalf("expected serve error to bubble up, got %v", err)
	}
}

func TestServeHTTPAndHTTPS(t *testing.T) {
	t.Run("http", func(t *testing.T) {
		server := &http.Server{
			Addr: "127.0.0.1:0",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("ok"))
			}),
		}
		mock := moxy.NewMockServerEngine(nil)

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		_ = listener.Close()
		server.Addr = addr

		errCh := make(chan error, 1)
		go func() {
			errCh <- serve(server, mock, false)
		}()

		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := http.Get("http://" + addr)
			if err == nil {
				_, _ = ioReadAllAndClose(resp)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("server did not start in time: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}

		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	})

	t.Run("https", func(t *testing.T) {
		mock := moxy.NewMockServerEngine(&moxy.Config{Protocol: moxy.HTTPS})
		server := &http.Server{
			Addr:    "127.0.0.1:0",
			Handler: mock.Handler(),
		}

		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.Addr().String()
		_ = listener.Close()
		server.Addr = addr

		errCh := make(chan error, 1)
		go func() {
			errCh <- serve(server, mock, true)
		}()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: mock.ServerTLSConfig(),
			},
		}

		deadline := time.Now().Add(2 * time.Second)
		for {
			resp, err := client.Get("https://" + addr + "/__moxy/health")
			if err == nil {
				_, _ = ioReadAllAndClose(resp)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("https server did not start in time: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}

		if err := server.Close(); err != nil {
			t.Fatal(err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	})
}

func ioReadAllAndClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
