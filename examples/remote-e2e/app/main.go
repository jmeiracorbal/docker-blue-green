package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed version.txt
var imageVersion string

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if len(args) != 1 {
		return errors.New("expected exactly one command")
	}
	switch args[0] {
	case "serve":
		server := &http.Server{Addr: ":8080", Handler: routes(), ReadHeaderTimeout: 5 * time.Second}
		return server.ListenAndServe()
	case "health":
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("healthcheck returned HTTP %d", response.StatusCode)
		}
		return nil
	case "before-start":
		if os.Getenv("E2E_FAIL_BEFORE_START") == "1" {
			return errors.New("injected before_start failure")
		}
	case "before-switch":
		if os.Getenv("E2E_FAIL_BEFORE_SWITCH") == "1" {
			return errors.New("injected before_switch failure")
		}
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	_, err := fmt.Fprintln(output, "ok")
	return err
}

func routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if os.Getenv("E2E_FAIL_HEALTH") == "1" {
			http.Error(w, "injected unhealthy service", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version": strings.TrimSpace(imageVersion),
			"slot":    os.Getenv("DEPLOYMENT_SLOT"),
		})
	})
	return mux
}
