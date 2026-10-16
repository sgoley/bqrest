package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"bqrest/internal/bqcatalog"
	"bqrest/internal/config"
	tea "charm.land/bubbletea/v2"
)

func testConfig() config.Config {
	return config.Config{
		Version: config.CurrentVersion,
		Connections: []config.Connection{{
			Name:               "analytics",
			ProjectID:          "example-project",
			Location:           "US",
			CredentialsFileEnv: "BQREST_CREDENTIALS_FILE",
			Datasets:           []string{"published_insights"},
			Resources: []config.Resource{{
				Dataset:        "published_insights",
				Table:          "monthly_revenue",
				EnabledColumns: []string{"month", "revenue"},
			}},
			Limits: config.QueryLimits{MaxBytesBilled: 1_000_000, MaxPageRows: 100, MaxResponseBytes: 1_000_000},
		}},
		Callers: []config.Caller{{
			Name:               "reporting-service",
			TokenEnv:           "BQREST_REPORTING_TOKEN",
			AllowedConnections: []string{"analytics"},
		}},
	}
}

func testModel(t *testing.T) model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.WriteAtomic(path, testConfig()); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return model{
		configPath: path,
		cfg:        testConfig(),
		lookup:     func(string) (string, bool) { return "", false },
	}
}

func key(code rune, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Text: text}
}

func press(t *testing.T, m model, msg tea.KeyPressMsg) model {
	t.Helper()
	updated, _ := m.Update(msg)
	result, ok := updated.(model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return result
}

func TestSaveWithoutChangesReportsNoPending(t *testing.T) {
	m := press(t, testModel(t), key('s', "s"))
	if m.mode == modeReview {
		t.Fatal("expected no review without changes")
	}
	if m.status != "No pending changes" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestSaveFlowShowsDiffThenWritesAtomically(t *testing.T) {
	m := testModel(t)
	m.cfg.Callers[0].AllowedConnections = append(m.cfg.Callers[0].AllowedConnections, "extra")
	// 'extra' is not a configured connection: review opens, save refuses.
	m = press(t, m, key('s', "s"))
	if m.mode != modeReview || m.review == nil {
		t.Fatalf("expected review mode, got mode=%d", m.mode)
	}
	if !strings.Contains(m.review.diff, `"extra"`) || !strings.Contains(m.review.diff, "+ ") {
		t.Fatalf("diff missing added grant:\n%s", m.review.diff)
	}
	m = press(t, m, key(tea.KeyEnter, ""))
	if m.mode != modeReview {
		t.Fatalf("invalid draft should stay in review, mode=%d", m.mode)
	}
	if !strings.Contains(m.status, "not configured") {
		t.Fatalf("expected validation error, status = %q", m.status)
	}

	// Cancel, fix the draft, save for real.
	m = press(t, m, key(tea.KeyEscape, ""))
	m.cfg.Callers[0].AllowedConnections = m.cfg.Callers[0].AllowedConnections[:1]
	m.cfg.Callers = append(m.cfg.Callers, config.Caller{
		Name:               "billing-export",
		TokenEnv:           "BQREST_BILLING_TOKEN",
		AllowedConnections: []string{"analytics"},
	})
	m = press(t, m, key('s', "s"))
	if m.mode != modeReview {
		t.Fatalf("expected review mode, mode=%d status=%q", m.mode, m.status)
	}
	m = press(t, m, key('y', "y"))
	if m.mode != modeBrowse {
		t.Fatalf("expected return to browse after save, mode=%d", m.mode)
	}
	onDisk, err := config.Read(m.configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if len(onDisk.Callers) != 2 || onDisk.Callers[1].Name != "billing-export" {
		t.Fatalf("saved config missing new caller: %+v", onDisk.Callers)
	}
}

func TestReviewTabTogglesFullConfigView(t *testing.T) {
	m := testModel(t)
	m.cfg.Connections[0].Location = "EU"
	m = press(t, m, key('s', "s"))
	if m.review == nil {
		t.Fatal("expected review")
	}
	m = press(t, m, key(tea.KeyTab, ""))
	if !m.review.showFull {
		t.Fatal("tab should switch to full config view")
	}
	if !strings.Contains(m.review.full, `"location": "EU"`) {
		t.Fatalf("full render missing pending value:\n%s", m.review.full)
	}
}

func TestConnectionFormEditsDraft(t *testing.T) {
	m := testModel(t)
	m = press(t, m, key('e', "e"))
	if m.mode != modeConnectionForm || m.form == nil {
		t.Fatal("expected connection form")
	}
	if m.form.fields[0].value != "analytics" {
		t.Fatalf("form not seeded: %+v", m.form.fields[0])
	}
	// Move to the Location field and replace it.
	m = press(t, m, key(tea.KeyDown, ""))
	m = press(t, m, key(tea.KeyDown, ""))
	field := &m.form.fields[2]
	field.value = ""
	field.cursor = 0
	m = press(t, m, key('E', "E"))
	m = press(t, m, key('U', "U"))
	m = press(t, m, key(tea.KeyEnter, ""))
	if m.mode != modeBrowse {
		t.Fatalf("expected browse after apply, mode=%d", m.mode)
	}
	if m.cfg.Connections[0].Location != "EU" {
		t.Fatalf("location = %q", m.cfg.Connections[0].Location)
	}
	// Allowlist survives guided editing.
	if len(m.cfg.Connections[0].Resources) != 1 {
		t.Fatal("edit dropped the resource allowlist")
	}
}

func TestConnectionFormRejectsNonNumericLimit(t *testing.T) {
	m := testModel(t)
	m = press(t, m, key('e', "e"))
	m.form.fields[5].value = "lots"
	m = press(t, m, key(tea.KeyEnter, ""))
	if m.mode != modeConnectionForm {
		t.Fatalf("expected to stay in form, mode=%d", m.mode)
	}
	if m.form.err == "" {
		t.Fatal("expected a form error")
	}
}

func TestCallerFormTogglesGrants(t *testing.T) {
	m := testModel(t)
	m.tab = 1
	m = press(t, m, key('e', "e"))
	if m.mode != modeCallerForm {
		t.Fatalf("expected caller form, mode=%d", m.mode)
	}
	if len(m.form.grants) != 1 || !m.form.grants[0].checked {
		t.Fatalf("grants not seeded: %+v", m.form.grants)
	}
	// Focus the grant row and uncheck it.
	m = press(t, m, key(tea.KeyDown, ""))
	m = press(t, m, key(tea.KeyDown, ""))
	m = press(t, m, key(tea.KeySpace, " "))
	m = press(t, m, key(tea.KeyEnter, ""))
	if len(m.cfg.Callers[0].AllowedConnections) != 0 {
		t.Fatalf("grant not removed: %+v", m.cfg.Callers[0])
	}
	if !strings.Contains(m.status, "draft") {
		t.Fatalf("expected draft warning, status = %q", m.status)
	}
}

func TestDatasetPickerTogglesAndSelects(t *testing.T) {
	m := testModel(t)
	m.tab = 2
	m = press(t, m, key('d', "d"))
	if m.mode != modeDatasets {
		t.Fatalf("expected dataset picker, mode=%d", m.mode)
	}
	// Simulate discovery results.
	m.discoveredDatasets = []string{"new_insights", "published_insights"}
	rows := m.datasetRows()
	if len(rows) != 2 || rows[0] != "published_insights" || rows[1] != "new_insights" {
		t.Fatalf("rows = %v", rows)
	}
	m = press(t, m, key(tea.KeyDown, ""))
	m = press(t, m, key(tea.KeySpace, " "))
	if len(m.cfg.Connections[0].Datasets) != 2 {
		t.Fatalf("dataset not configured: %v", m.cfg.Connections[0].Datasets)
	}
	m = press(t, m, key(tea.KeyEnter, ""))
	if m.mode != modeBrowse || m.selectedDataset() != "new_insights" {
		t.Fatalf("expected browse of new_insights, mode=%d dataset=%q", m.mode, m.selectedDataset())
	}
}

func TestDatasetPickerRemovalDropsResources(t *testing.T) {
	m := testModel(t)
	m.tab = 2
	m = press(t, m, key('d', "d"))
	m.discoveredDatasets = []string{}
	// Cursor is on the configured dataset; unconfigure it.
	m = press(t, m, key(tea.KeySpace, " "))
	conn := m.cfg.Connections[0]
	if len(conn.Datasets) != 0 || len(conn.Resources) != 0 {
		t.Fatalf("expected empty datasets/resources, got %+v", conn)
	}
}

func TestDeleteConnectionStripsGrants(t *testing.T) {
	m := testModel(t)
	m = press(t, m, key('x', "x"))
	if m.mode != modeConfirm {
		t.Fatalf("expected confirm, mode=%d", m.mode)
	}
	m = press(t, m, key('y', "y"))
	if len(m.cfg.Connections) != 0 {
		t.Fatal("connection not deleted")
	}
	if len(m.cfg.Callers[0].AllowedConnections) != 0 {
		t.Fatal("dangling grant left behind")
	}
}

func TestCatalogLoadedMsgUpdatesSelection(t *testing.T) {
	m := testModel(t)
	m.tab = 2
	m.loadingCatalog = true
	updated, _ := m.Update(catalogLoadedMsg{
		connection: "analytics",
		dataset:    "published_insights",
		resources:  []bqcatalog.Resource{{Table: "daily_sales", Type: "TABLE"}},
	})
	m = updated.(model)
	if len(m.catalog) != 1 || m.catalog[0].Table != "daily_sales" {
		t.Fatalf("catalog = %+v", m.catalog)
	}
}
