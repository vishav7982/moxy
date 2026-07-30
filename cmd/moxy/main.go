package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/vishav7982/moxy"
)

func main() {
	if err := run(os.Args[1:], log.Default(), serve); err != nil {
		log.Fatal(err)
	}
}

func run(args []string, logger *log.Logger, serveFn func(*http.Server, *moxy.MockServer, bool) error) error {
	flags := flag.NewFlagSet("moxy", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	host := flags.String("host", "127.0.0.1", "host to bind")
	port := flags.Int("port", 8080, "port to bind")
	https := flags.Bool("https", false, "serve HTTPS with a generated self-signed certificate")
	mappings := flags.String("mappings", "", "directory containing moxy JSON mappings")
	verbose := flags.Bool("verbose", false, "enable verbose request logging")
	if err := flags.Parse(args); err != nil {
		return err
	}

	protocol := moxy.HTTP
	if *https {
		protocol = moxy.HTTPS
	}
	mock := moxy.NewMockServerEngine(&moxy.Config{
		Protocol:       protocol,
		VerboseLogging: *verbose,
	})
	if *mappings != "" {
		if err := mock.LoadMappings(*mappings); err != nil {
			return fmt.Errorf("failed to load mappings: %w", err)
		}
	}

	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	server := &http.Server{
		Addr:    addr,
		Handler: mock.Handler(),
	}

	scheme := "http"
	if *https {
		scheme = "https"
	}
	logger.Printf("moxy listening on %s://%s", scheme, addr)
	return serveFn(server, mock, *https)
}

func serve(server *http.Server, mock *moxy.MockServer, https bool) error {
	if https {
		listener, err := net.Listen("tcp", server.Addr)
		if err != nil {
			return fmt.Errorf("listen failed: %w", err)
		}
		tlsListener := tls.NewListener(listener, mock.ServerTLSConfig())
		if err := server.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server failed: %w", err)
		}
		return nil
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}
