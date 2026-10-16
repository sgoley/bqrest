package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHealthzIntegration(t *testing.T) {
	server := httptest.NewServer(newHandler())
	defer server.Close()

	response, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz response: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("health status = %q, want ok", body.Status)
	}
}

func TestUnknownRouteIsNotServed(t *testing.T) {
	server := httptest.NewServer(newHandler())
	defer server.Close()

	response, err := http.Get(server.URL + "/unimplemented")
	if err != nil {
		t.Fatalf("GET /unimplemented: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /unimplemented status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func writeRuntimeFixture(t *testing.T) (configPath string, env map[string]string) {
	t.Helper()
	dir := t.TempDir()
	credentialsFile := filepath.Join(dir, "service-account.json")
	if err := os.WriteFile(credentialsFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write credentials fixture: %v", err)
	}
	configPath = filepath.Join(dir, "config.json")
	configJSON := `{
  "version": 1,
  "connections": [{
    "name": "analytics",
    "project_id": "example-project",
    "location": "US",
    "credentials_file_env": "TEST_CREDENTIALS_FILE",
    "datasets": ["published_insights"],
    "resources": [{"dataset": "published_insights", "table": "monthly_revenue", "enabled_columns": ["month"]}],
    "limits": {"max_bytes_billed": 1000000, "max_page_rows": 100, "max_response_bytes": 1000000}
  }],
  "callers": [{"name": "test-caller", "token_env": "TEST_CALLER_TOKEN", "allowed_connections": ["analytics"]}]
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return configPath, map[string]string{
		"TEST_CREDENTIALS_FILE": credentialsFile,
		"TEST_CALLER_TOKEN":     "test-token",
	}
}

func TestValidateSubcommandAcceptsValidRuntime(t *testing.T) {
	configPath, env := writeRuntimeFixture(t)
	var out bytes.Buffer
	err := runValidate([]string{"-config", configPath}, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}, &out)
	if err != nil {
		t.Fatalf("runValidate() error = %v", err)
	}
	if !strings.Contains(out.String(), "configuration valid: 1 connection(s), 1 caller(s)") {
		t.Fatalf("runValidate() output = %q", out.String())
	}
}

func TestValidateSubcommandReportsActionableError(t *testing.T) {
	configPath, env := writeRuntimeFixture(t)
	delete(env, "TEST_CALLER_TOKEN")
	err := runValidate([]string{"-config", configPath}, func(name string) (string, bool) {
		value, ok := env[name]
		return value, ok
	}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runValidate() succeeded without the caller token")
	}
	if !strings.Contains(err.Error(), "TEST_CALLER_TOKEN") {
		t.Fatalf("runValidate() error = %q, want it to name the missing environment variable", err)
	}
}

// TestServiceStartsFromExampleConfig builds the real binary and boots it with
// the documented example configuration to prove the shipped artifact starts
// and serves health without leaking connection metadata.
func TestServiceStartsFromExampleConfig(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	binary := filepath.Join(t.TempDir(), "bqrest")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}

	credentialsFile := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(credentialsFile, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write credentials fixture: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release test port: %v", err)
	}

	exampleConfig := filepath.Join("..", "..", "config.example.json")
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(),
		"BQREST_CONFIG="+exampleConfig,
		"BQREST_LISTEN="+addr,
		"BQREST_ANALYTICS_CREDENTIALS_FILE="+credentialsFile,
		"BQREST_REPORTING_TOKEN=test-token",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start service: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		response, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			var status struct {
				Status string `json:"status"`
			}
			if decodeErr := json.NewDecoder(response.Body).Decode(&status); decodeErr != nil {
				response.Body.Close()
				t.Fatalf("decode /healthz: %v", decodeErr)
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK && status.Status == "ok" {
				break
			}
			t.Fatalf("/healthz status = %d body status = %q", response.StatusCode, status.Status)
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not become healthy: %v\nstderr: %s", err, stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal service: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("graceful shutdown: %v\nstderr: %s", err, stderr.String())
	}
	for _, leaked := range []string{credentialsFile, "test-token", "example-gcp-project"} {
		if strings.Contains(stderr.String(), leaked) {
			t.Fatalf("service logs leak %q\nstderr: %s", leaked, stderr.String())
		}
	}
}
