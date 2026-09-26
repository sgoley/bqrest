package tui

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"bqrest/internal/bqcatalog"
	"bqrest/internal/config"
	tea "charm.land/bubbletea/v2"
)

type model struct {
	configPath       string
	cfg              config.Config
	lookup           config.LookupEnv
	tab              int
	connectionIndex  int
	datasetIndex     int
	catalog          []bqcatalog.Resource
	selectedResource int
	selectedColumn   int
	editingColumns   bool
	loading          bool
	status           string
}

type catalogLoadedMsg struct {
	connection string
	dataset    string
	resources  []bqcatalog.Resource
	err        error
}

var tabs = []string{"Connections", "Consumers", "Resources"}

func Run(args []string, lookup config.LookupEnv) error {
	flags := flag.NewFlagSet("bqrest tui", flag.ContinueOnError)
	configPath := flags.String("config", envOr("BQREST_CONFIG", "./config.json"), "path to the bqrest JSON configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Read(*configPath)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}
	for i := range cfg.Connections {
		if len(cfg.Connections[i].Datasets) == 0 {
			cfg.Connections[i].Datasets = datasetsFromResources(cfg.Connections[i].Resources)
		}
	}
	program := tea.NewProgram(model{configPath: *configPath, cfg: cfg, lookup: lookup})
	_, err = program.Run()
	return err
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case catalogLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.status = "BigQuery metadata request failed; check the credential file and dataset metadata permissions"
			return m, nil
		}
		if !m.hasSelection() || m.selectedConnection().Name != msg.connection || m.selectedDataset() != msg.dataset {
			return m, nil
		}
		m.catalog = msg.resources
		if m.selectedResource >= len(m.catalog) {
			m.selectedResource = max(0, len(m.catalog)-1)
		}
		m.status = fmt.Sprintf("Loaded %d tables/views", len(m.catalog))
		return m, nil
	case tea.KeyPressMsg:
		if m.editingColumns {
			switch msg.String() {
			case "q", "ctrl+c", "esc":
				m.editingColumns = false
				m.status = "Returned to resource list"
				if msg.String() == "q" || msg.String() == "ctrl+c" {
					return m, tea.Quit
				}
				return m, nil
			case "up":
				m.moveSelection(-1)
			case "down":
				m.moveSelection(1)
			case "space":
				m.toggleColumn()
			case "enter":
				m.editingColumns = false
				m.status = "Column selection updated"
			}
			return m, nil
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			return m, tea.Quit
		case "1":
			m.tab = 0
		case "2":
			m.tab = 1
		case "3":
			m.tab = 2
		case "tab", "right":
			m.tab = (m.tab + 1) % len(tabs)
		case "shift+tab", "left":
			m.tab = (m.tab + len(tabs) - 1) % len(tabs)
		case "up":
			m.moveSelection(-1)
		case "down":
			m.moveSelection(1)
		case "c":
			m.nextConnection()
		case "d":
			m.nextDataset()
		case "r":
			if m.tab == 2 && !m.editingColumns && !m.loading {
				m.loading = true
				m.status = "Loading BigQuery metadata…"
				return m, m.refreshCatalog()
			}
		case "space":
			if m.tab == 2 {
				if m.editingColumns {
					m.toggleColumn()
				} else {
					m.toggleResource()
				}
			}
		case "enter":
			if m.tab == 2 {
				if m.editingColumns {
					m.editingColumns = false
					m.status = "Column selection updated"
				} else if m.selectedIsEnabled() {
					m.editingColumns = true
					m.selectedColumn = 0
					m.status = "Space toggles columns; Enter returns"
				} else {
					m.status = "Enable the table/view with Space before choosing columns"
				}
			}
		case "s":
			if m.tab == 2 {
				if err := m.save(); err != nil {
					m.status = "Config not saved: " + err.Error()
				} else {
					m.status = "Config saved atomically. Restart bqrest to load the new allowlist."
				}
			}
		}
	}
	return m, nil
}

func (m model) View() tea.View {
	var b strings.Builder
	fmt.Fprintf(&b, "bqrest admin  ·  %s\n", m.configPath)
	b.WriteString("────────────────────────────────────────────────────────────────────────────\n")
	for i, tab := range tabs {
		if i == m.tab {
			fmt.Fprintf(&b, "  [%d %s]", i+1, tab)
		} else {
			fmt.Fprintf(&b, "   %d %s ", i+1, tab)
		}
	}
	b.WriteString("\n\n")
	switch m.tab {
	case 0:
		m.viewConnections(&b)
	case 1:
		m.viewCallers(&b)
	case 2:
		m.viewResources(&b)
	}
	if m.status != "" {
		fmt.Fprintf(&b, "\n  %s\n", m.status)
	}
	b.WriteString("\n────────────────────────────────────────────────────────────────────────────\n")
	if m.tab == 2 {
		b.WriteString("  c connection  d dataset  r refresh  ↑/↓ select  Space expose/hide  Enter columns  s save  q quit\n")
	} else {
		b.WriteString("  1–3 switch views   tab/←/→ navigate   q quit\n")
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m model) viewConnections(b *strings.Builder) {
	b.WriteString("CONNECTIONS\n\n")
	if len(m.cfg.Connections) == 0 {
		b.WriteString("  No connections configured.\n")
		return
	}
	for _, connection := range m.cfg.Connections {
		fmt.Fprintf(b, "  %s\n", connection.Name)
		fmt.Fprintf(b, "    BigQuery project: %s    Location: %s\n", connection.ProjectID, connection.Location)
		fmt.Fprintf(b, "    Datasets: %s\n", strings.Join(connection.Datasets, ", "))
		fmt.Fprintf(b, "    Credential reference: %s (%s)\n", connection.CredentialsFileEnv, m.credentialStatus(connection.CredentialsFileEnv))
		fmt.Fprintf(b, "    Limits: %d rows/page, %d response bytes, %d bytes billed\n\n", connection.Limits.MaxPageRows, connection.Limits.MaxResponseBytes, connection.Limits.MaxBytesBilled)
	}
}

func (m model) viewCallers(b *strings.Builder) {
	b.WriteString("CONSUMERS & GRANTS\n\n")
	if len(m.cfg.Callers) == 0 {
		b.WriteString("  No consumers configured.\n")
		return
	}
	for _, caller := range m.cfg.Callers {
		fmt.Fprintf(b, "  %s\n", caller.Name)
		fmt.Fprintf(b, "    Token reference: %s (%s)\n", caller.TokenEnv, m.envStatus(caller.TokenEnv))
		fmt.Fprintf(b, "    Granted connections: %s\n\n", strings.Join(caller.AllowedConnections, ", "))
	}
	b.WriteString("  Secret values are never displayed.\n")
}

func (m model) viewResources(b *strings.Builder) {
	b.WriteString("TABLE & COLUMN EXPOSURE\n\n")
	if !m.hasSelection() {
		b.WriteString("  No connection or dataset is configured.\n")
		return
	}
	connection := m.selectedConnection()
	dataset := m.selectedDataset()
	fmt.Fprintf(b, "  Connection: %s   Project: %s   Dataset: %s\n", connection.Name, connection.ProjectID, dataset)
	fmt.Fprintf(b, "  Credential reference: %s (%s)\n\n", connection.CredentialsFileEnv, m.credentialStatus(connection.CredentialsFileEnv))
	if m.editingColumns {
		m.viewColumns(b)
		return
	}
	if m.loading {
		b.WriteString("  Loading tables and views from BigQuery…\n")
		return
	}
	if m.catalog == nil {
		b.WriteString("  Press r to load available tables and views.\n")
		return
	}
	if len(m.catalog) == 0 {
		b.WriteString("  No tables or views found in this dataset.\n")
		return
	}
	for i, item := range m.catalog {
		marker := " "
		if i == m.selectedResource {
			marker = ">"
		}
		resource, enabled := m.resourceConfig(item.Table)
		check := "[ ]"
		columnCount := 0
		if enabled {
			check = "[x]"
			columnCount = len(resource.EnabledColumns)
		}
		fmt.Fprintf(b, "  %s %s %-20s %-17s %d column(s)\n", marker, check, item.Table, item.Type, columnCount)
	}
	fmt.Fprintf(b, "\n  %d table/view(s) in dataset; only checked resources and their checked columns are exposed.\n", len(m.catalog))
}

func (m model) viewColumns(b *strings.Builder) {
	item := m.selectedCatalogResource()
	if item == nil {
		b.WriteString("  No table/view selected.\n")
		return
	}
	resource, enabled := m.resourceConfig(item.Table)
	fmt.Fprintf(b, "  ENABLED COLUMNS · %s (%s)\n\n", item.Table, item.Type)
	if len(item.Columns) == 0 {
		b.WriteString("  BigQuery returned no top-level columns.\n")
		return
	}
	for i, column := range item.Columns {
		marker := " "
		if i == m.selectedColumn {
			marker = ">"
		}
		checked := "[ ]"
		if enabled && contains(resource.EnabledColumns, column.Name) {
			checked = "[x]"
		}
		fmt.Fprintf(b, "  %s %s %-28s %s\n", marker, checked, column.Name, column.Type)
	}
	b.WriteString("\n  Only checked columns will be included in API responses.\n")
}

func (m *model) moveSelection(delta int) {
	if m.tab != 2 {
		return
	}
	if m.editingColumns {
		item := m.selectedCatalogResource()
		if item == nil || len(item.Columns) == 0 {
			return
		}
		m.selectedColumn = clamp(m.selectedColumn+delta, 0, len(item.Columns)-1)
		return
	}
	if len(m.catalog) > 0 {
		m.selectedResource = clamp(m.selectedResource+delta, 0, len(m.catalog)-1)
	}
}

func (m *model) nextConnection() {
	if len(m.cfg.Connections) < 2 {
		return
	}
	m.connectionIndex = (m.connectionIndex + 1) % len(m.cfg.Connections)
	m.datasetIndex = 0
	m.catalog = nil
	m.selectedResource = 0
	m.status = "Press r to load this connection's catalog"
}

func (m *model) nextDataset() {
	if !m.hasSelection() {
		return
	}
	datasets := m.selectedConnection().Datasets
	if len(datasets) < 2 {
		return
	}
	m.datasetIndex = (m.datasetIndex + 1) % len(datasets)
	m.catalog = nil
	m.selectedResource = 0
	m.status = "Press r to load this dataset's catalog"
}

func (m *model) refreshCatalog() tea.Cmd {
	if !m.hasSelection() {
		return nil
	}
	connection := m.selectedConnection()
	dataset := m.selectedDataset()
	credentialsPath, ok := m.lookup(connection.CredentialsFileEnv)
	if !ok || strings.TrimSpace(credentialsPath) == "" {
		return func() tea.Msg {
			return catalogLoadedMsg{connection: connection.Name, dataset: dataset, err: fmt.Errorf("environment variable %s is not set", connection.CredentialsFileEnv)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resources, err := bqcatalog.List(ctx, connection, dataset, credentialsPath)
		return catalogLoadedMsg{connection: connection.Name, dataset: dataset, resources: resources, err: err}
	}
}

func (m *model) toggleResource() {
	item := m.selectedCatalogResource()
	if item == nil || !m.hasSelection() {
		return
	}
	connection := &m.cfg.Connections[m.connectionIndex]
	for i, resource := range connection.Resources {
		if resource.Dataset == m.selectedDataset() && resource.Table == item.Table {
			connection.Resources = append(connection.Resources[:i], connection.Resources[i+1:]...)
			m.status = fmt.Sprintf("%s is no longer exposed", item.Table)
			return
		}
	}
	connection.Resources = append(connection.Resources, config.Resource{Dataset: m.selectedDataset(), Table: item.Table})
	m.status = fmt.Sprintf("%s enabled; press Enter to choose its columns", item.Table)
}

func (m *model) toggleColumn() {
	item := m.selectedCatalogResource()
	if item == nil || !m.hasSelection() || m.selectedColumn >= len(item.Columns) {
		return
	}
	connection := &m.cfg.Connections[m.connectionIndex]
	for resourceIndex := range connection.Resources {
		resource := &connection.Resources[resourceIndex]
		if resource.Dataset != m.selectedDataset() || resource.Table != item.Table {
			continue
		}
		columnName := item.Columns[m.selectedColumn].Name
		for i, column := range resource.EnabledColumns {
			if column == columnName {
				if len(resource.EnabledColumns) == 1 {
					m.status = "A resource must expose at least one column"
					return
				}
				resource.EnabledColumns = append(resource.EnabledColumns[:i], resource.EnabledColumns[i+1:]...)
				return
			}
		}
		resource.EnabledColumns = append(resource.EnabledColumns, columnName)
		return
	}
}

func (m *model) save() error {
	if err := m.cfg.ValidateStructure(); err != nil {
		return err
	}
	return config.WriteAtomic(m.configPath, m.cfg)
}

func (m model) hasSelection() bool {
	return m.connectionIndex >= 0 && m.connectionIndex < len(m.cfg.Connections) && len(m.selectedConnection().Datasets) > 0
}

func (m model) selectedConnection() config.Connection {
	if m.connectionIndex < 0 || m.connectionIndex >= len(m.cfg.Connections) {
		return config.Connection{}
	}
	return m.cfg.Connections[m.connectionIndex]
}

func (m model) selectedDataset() string {
	connection := m.selectedConnection()
	if len(connection.Datasets) == 0 {
		return ""
	}
	return connection.Datasets[clamp(m.datasetIndex, 0, len(connection.Datasets)-1)]
}

func (m model) selectedCatalogResource() *bqcatalog.Resource {
	if m.selectedResource < 0 || m.selectedResource >= len(m.catalog) {
		return nil
	}
	return &m.catalog[m.selectedResource]
}

func (m model) selectedIsEnabled() bool {
	item := m.selectedCatalogResource()
	if item == nil {
		return false
	}
	_, enabled := m.resourceConfig(item.Table)
	return enabled
}

func (m model) resourceConfig(table string) (config.Resource, bool) {
	if !m.hasSelection() {
		return config.Resource{}, false
	}
	for _, resource := range m.selectedConnection().Resources {
		if resource.Dataset == m.selectedDataset() && resource.Table == table {
			return resource, true
		}
	}
	return config.Resource{}, false
}

func (m model) credentialStatus(envName string) string {
	path, ok := m.lookup(envName)
	if !ok || strings.TrimSpace(path) == "" {
		return "missing"
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "unavailable"
	}
	return "mounted"
}

func (m model) envStatus(name string) string {
	value, ok := m.lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "missing"
	}
	return "set"
}

func datasetsFromResources(resources []config.Resource) []string {
	seen := make(map[string]struct{}, len(resources))
	var datasets []string
	for _, resource := range resources {
		if _, ok := seen[resource.Dataset]; ok {
			continue
		}
		seen[resource.Dataset] = struct{}{}
		datasets = append(datasets, resource.Dataset)
	}
	return datasets
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
