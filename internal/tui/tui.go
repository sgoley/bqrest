package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"bqrest/internal/bqcatalog"
	"bqrest/internal/config"
	tea "charm.land/bubbletea/v2"
)

type mode int

const (
	modeBrowse mode = iota
	modeColumns
	modeDatasets
	modeConnectionForm
	modeCallerForm
	modeReview
	modeConfirm
)

type model struct {
	configPath string
	cfg        config.Config
	lookup     config.LookupEnv

	tab  int
	mode mode

	connectionIndex int
	callerIndex     int
	datasetIndex    int

	catalog          []bqcatalog.Resource
	selectedResource int
	selectedColumn   int
	loadingCatalog   bool

	discoveredDatasets []string
	datasetCursor      int
	loadingDatasets    bool

	form    *formState
	review  *reviewState
	confirm *confirmState

	height int
	status string
}

type catalogLoadedMsg struct {
	connection string
	dataset    string
	resources  []bqcatalog.Resource
	err        error
}

type datasetsLoadedMsg struct {
	connection string
	datasets   []string
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
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read configuration: %w", err)
		}
		cfg = config.Config{Version: config.CurrentVersion}
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
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case catalogLoadedMsg:
		m.loadingCatalog = false
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
	case datasetsLoadedMsg:
		m.loadingDatasets = false
		if msg.err != nil {
			m.status = "BigQuery dataset listing failed; check the credential file and project metadata permissions"
			return m, nil
		}
		if !m.hasConnection() || m.selectedConnection().Name != msg.connection {
			return m, nil
		}
		m.discoveredDatasets = msg.datasets
		m.status = fmt.Sprintf("Discovered %d dataset(s) in project %s", len(msg.datasets), m.selectedConnection().ProjectID)
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeColumns:
			return m.updateColumns(msg)
		case modeDatasets:
			return m.updateDatasetPicker(msg)
		case modeConnectionForm, modeCallerForm:
			return m.updateForm(msg)
		case modeReview:
			return m.updateReview(msg)
		case modeConfirm:
			return m.updateConfirm(msg)
		default:
			return m.updateBrowse(msg)
		}
	}
	return m, nil
}

func (m model) updateBrowse(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
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
	case "a":
		switch m.tab {
		case 0:
			m.form = newConnectionForm(nil, -1)
			m.mode = modeConnectionForm
		case 1:
			m.form = newCallerForm(nil, -1, m.cfg.Connections)
			m.mode = modeCallerForm
		}
	case "e", "enter":
		switch m.tab {
		case 0:
			if m.connectionIndex < len(m.cfg.Connections) {
				conn := m.cfg.Connections[m.connectionIndex]
				m.form = newConnectionForm(&conn, m.connectionIndex)
				m.mode = modeConnectionForm
			}
		case 1:
			if m.callerIndex < len(m.cfg.Callers) {
				caller := m.cfg.Callers[m.callerIndex]
				m.form = newCallerForm(&caller, m.callerIndex, m.cfg.Connections)
				m.mode = modeCallerForm
			}
		case 2:
			if m.selectedIsEnabled() {
				m.mode = modeColumns
				m.selectedColumn = 0
				m.status = "Space toggles columns; Enter returns"
			} else {
				m.status = "Enable the table/view with Space before choosing columns"
			}
		}
	case "x":
		switch m.tab {
		case 0:
			if m.connectionIndex < len(m.cfg.Connections) {
				name := m.cfg.Connections[m.connectionIndex].Name
				m.confirm = &confirmState{
					prompt: fmt.Sprintf("Delete connection %q? Its allowlist and consumer grants are removed too.", name),
					apply:  deleteConnection(m.connectionIndex),
				}
				m.mode = modeConfirm
			}
		case 1:
			if m.callerIndex < len(m.cfg.Callers) {
				m.confirm = &confirmState{
					prompt: fmt.Sprintf("Delete consumer %q?", m.cfg.Callers[m.callerIndex].Name),
					apply:  deleteCaller(m.callerIndex),
				}
				m.mode = modeConfirm
			}
		}
	case "c":
		if m.tab == 2 {
			m.nextConnection()
		}
	case "d":
		if m.tab == 2 {
			if !m.hasConnection() {
				m.status = "Configure a connection first (tab 1, press a)"
				return m, nil
			}
			m.mode = modeDatasets
			m.datasetCursor = 0
			if m.discoveredDatasets == nil {
				m.loadingDatasets = true
				return m, m.loadDatasets()
			}
		}
	case "r":
		if m.tab == 2 && !m.loadingCatalog {
			if !m.hasSelection() {
				m.status = "Configure a connection and dataset first"
				return m, nil
			}
			m.loadingCatalog = true
			m.status = "Loading BigQuery metadata…"
			return m, m.refreshCatalog()
		}
	case "space":
		if m.tab == 2 {
			m.toggleResource()
		}
	case "s":
		m.openReview()
	}
	return m, nil
}

func (m model) updateColumns(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc", "enter":
		m.mode = modeBrowse
		m.status = "Returned to resource list"
	case "up":
		if item := m.selectedCatalogResource(); item != nil && len(item.Columns) > 0 {
			m.selectedColumn = clamp(m.selectedColumn-1, 0, len(item.Columns)-1)
		}
	case "down":
		if item := m.selectedCatalogResource(); item != nil && len(item.Columns) > 0 {
			m.selectedColumn = clamp(m.selectedColumn+1, 0, len(item.Columns)-1)
		}
	case "space":
		m.toggleColumn()
	}
	return m, nil
}

func (m *model) moveSelection(delta int) {
	switch m.tab {
	case 0:
		if len(m.cfg.Connections) > 0 {
			m.connectionIndex = clamp(m.connectionIndex+delta, 0, len(m.cfg.Connections)-1)
		}
	case 1:
		if len(m.cfg.Callers) > 0 {
			m.callerIndex = clamp(m.callerIndex+delta, 0, len(m.cfg.Callers)-1)
		}
	case 2:
		if len(m.catalog) > 0 {
			m.selectedResource = clamp(m.selectedResource+delta, 0, len(m.catalog)-1)
		}
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
	m.discoveredDatasets = nil
	m.status = "Press r to load this connection's catalog"
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

func (m *model) loadDatasets() tea.Cmd {
	if !m.hasConnection() {
		return nil
	}
	connection := m.selectedConnection()
	credentialsPath, ok := m.lookup(connection.CredentialsFileEnv)
	if !ok || strings.TrimSpace(credentialsPath) == "" {
		return func() tea.Msg {
			return datasetsLoadedMsg{connection: connection.Name, err: fmt.Errorf("environment variable %s is not set", connection.CredentialsFileEnv)}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		datasets, err := bqcatalog.ListDatasets(ctx, connection, credentialsPath)
		return datasetsLoadedMsg{connection: connection.Name, datasets: datasets, err: err}
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

// openReview renders a redacted diff between the on-disk config and the draft,
// then asks for confirmation before anything is written.
func (m *model) openReview() {
	disk, err := config.Read(m.configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.status = "Cannot review changes: " + err.Error()
			return
		}
		disk = config.Config{}
	}
	diff, err := config.Diff(disk, m.cfg)
	if err != nil {
		m.status = "Cannot review changes: " + err.Error()
		return
	}
	if diff == "" {
		m.status = "No pending changes"
		return
	}
	full, err := config.RenderRedacted(m.cfg)
	if err != nil {
		m.status = "Cannot render pending config: " + err.Error()
		return
	}
	m.review = &reviewState{diff: diff, full: full}
	m.mode = modeReview
	m.status = ""
}

func (m *model) confirmSave() {
	if err := m.cfg.ValidateStructure(); err != nil {
		m.status = "Config not saved: " + err.Error()
		return
	}
	if err := config.WriteAtomic(m.configPath, m.cfg); err != nil {
		m.status = "Config not saved: " + err.Error()
		return
	}
	m.review = nil
	m.mode = modeBrowse
	m.status = "Config saved atomically. Restart bqrest to load the new configuration."
}

func (m model) hasConnection() bool {
	return m.connectionIndex >= 0 && m.connectionIndex < len(m.cfg.Connections)
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
