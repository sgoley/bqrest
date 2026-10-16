package config

import (
	"strings"
	"testing"
)

func TestMarshalMatchesWriteAtomicFormat(t *testing.T) {
	data, err := Marshal(validTestConfig())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("Marshal() output must end with a newline")
	}
	if !strings.Contains(string(data), "\"version\": 1") {
		t.Fatalf("Marshal() output missing version field:\n%s", data)
	}
}

func TestDiffReturnsEmptyForIdenticalConfigs(t *testing.T) {
	diff, err := Diff(validTestConfig(), validTestConfig())
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if diff != "" {
		t.Fatalf("Diff() = %q, want empty", diff)
	}
}

func TestDiffShowsChangedLinesWithContext(t *testing.T) {
	before := validTestConfig()
	after := validTestConfig()
	after.Connections[0].Resources[0].EnabledColumns = append(after.Connections[0].Resources[0].EnabledColumns, "country")

	diff, err := Diff(before, after)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	if !strings.Contains(diff, "+             \"country\"") {
		t.Fatalf("Diff() missing added column line:\n%s", diff)
	}
	if !strings.Contains(diff, "-             \"revenue\"") {
		t.Fatalf("Diff() missing removed column line:\n%s", diff)
	}
	if !strings.Contains(diff, "--- on disk") || !strings.Contains(diff, "+++ pending") {
		t.Fatalf("Diff() missing header:\n%s", diff)
	}
	if strings.Contains(diff, "\"callers\"") {
		t.Fatalf("Diff() should collapse unchanged sections, got:\n%s", diff)
	}
	if !strings.Contains(diff, "unchanged line") {
		t.Fatalf("Diff() should summarize unchanged regions, got:\n%s", diff)
	}
}

func TestRenderRedactedMasksSensitiveValuesOnly(t *testing.T) {
	cfg := validTestConfig()
	rendered, err := RenderRedacted(cfg)
	if err != nil {
		t.Fatalf("RenderRedacted() error = %v", err)
	}
	// _env fields are references, not values: they must stay readable.
	if !strings.Contains(rendered, "BQREST_REPORTING_TOKEN") {
		t.Fatalf("RenderRedacted() masked an env reference:\n%s", rendered)
	}
}

func TestRedactLineMasksSecretValues(t *testing.T) {
	line := `      "private_key": "-----BEGIN PRIVATE KEY-----\nabc"`
	if got := redactLine(line); strings.Contains(got, "BEGIN") {
		t.Fatalf("redactLine() left secret material: %s", got)
	}
	ref := `      "credentials_file_env": "BQREST_CREDENTIALS_FILE",`
	if got := redactLine(ref); got != ref {
		t.Fatalf("redactLine() mangled a reference field: %s", got)
	}
}
