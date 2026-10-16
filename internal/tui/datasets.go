package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// datasetRows returns configured datasets first, then datasets discovered in
// the project that are not yet configured. Checkmarks track membership in the
// connection's configured list.
func (m model) datasetRows() []string {
	connection := m.selectedConnection()
	seen := make(map[string]struct{}, len(connection.Datasets))
	rows := make([]string, 0, len(connection.Datasets)+len(m.discoveredDatasets))
	for _, dataset := range connection.Datasets {
		seen[dataset] = struct{}{}
		rows = append(rows, dataset)
	}
	for _, dataset := range m.discoveredDatasets {
		if _, ok := seen[dataset]; !ok {
			rows = append(rows, dataset)
		}
	}
	return rows
}

func (m model) updateDatasetPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc":
		m.mode = modeBrowse
		m.status = "Returned to resource list"
	case "up":
		m.datasetCursor = clamp(m.datasetCursor-1, 0, max(0, len(m.datasetRows())-1))
	case "down":
		m.datasetCursor = clamp(m.datasetCursor+1, 0, max(0, len(m.datasetRows())-1))
	case "r":
		if !m.loadingDatasets {
			m.loadingDatasets = true
			m.status = "Discovering datasets…"
			return m, m.loadDatasets()
		}
	case "space":
		m.toggleDataset()
	case "enter":
		rows := m.datasetRows()
		if m.datasetCursor >= len(rows) {
			return m, nil
		}
		dataset := rows[m.datasetCursor]
		connection := m.selectedConnection()
		for i, configured := range connection.Datasets {
			if configured == dataset {
				m.datasetIndex = i
				m.catalog = nil
				m.selectedResource = 0
				m.mode = modeBrowse
				m.status = fmt.Sprintf("Browsing dataset %s; press r to load its catalog", dataset)
				return m, nil
			}
		}
		m.status = "Press Space to configure this dataset first"
	}
	return m, nil
}

// toggleDataset adds or removes the highlighted dataset from the selected
// connection's configured list. Removing a dataset also drops its allowlisted
// resources so the draft stays consistent.
func (m *model) toggleDataset() {
	rows := m.datasetRows()
	if m.datasetCursor >= len(rows) || m.connectionIndex >= len(m.cfg.Connections) {
		return
	}
	dataset := rows[m.datasetCursor]
	connection := &m.cfg.Connections[m.connectionIndex]
	for i, configured := range connection.Datasets {
		if configured == dataset {
			connection.Datasets = append(connection.Datasets[:i], connection.Datasets[i+1:]...)
			removed := 0
			kept := connection.Resources[:0]
			for _, resource := range connection.Resources {
				if resource.Dataset == dataset {
					removed++
					continue
				}
				kept = append(kept, resource)
			}
			connection.Resources = kept
			m.datasetIndex = clamp(m.datasetIndex, 0, max(0, len(connection.Datasets)-1))
			m.catalog = nil
			m.status = fmt.Sprintf("Dataset %s unconfigured; %d resource(s) dropped", dataset, removed)
			return
		}
	}
	connection.Datasets = append(connection.Datasets, dataset)
	m.status = fmt.Sprintf("Dataset %s configured; Enter selects it for browsing", dataset)
}

func (m model) viewDatasetPicker(b *strings.Builder) {
	connection := m.selectedConnection()
	fmt.Fprintf(b, "  DATASETS · %s · project %s\n\n", connection.Name, connection.ProjectID)
	fmt.Fprintf(b, "  Credential reference: %s (%s)\n\n", connection.CredentialsFileEnv, m.credentialStatus(connection.CredentialsFileEnv))
	rows := m.datasetRows()
	if len(rows) == 0 {
		if m.loadingDatasets {
			b.WriteString("  Discovering datasets in the project…\n")
		} else {
			b.WriteString("  No datasets. Press r to discover datasets the credential can see.\n")
		}
		return
	}
	configured := make(map[string]struct{}, len(connection.Datasets))
	for _, dataset := range connection.Datasets {
		configured[dataset] = struct{}{}
	}
	for i, dataset := range rows {
		marker := " "
		if i == m.datasetCursor {
			marker = ">"
		}
		check := "[ ]"
		label := "discovered"
		if _, ok := configured[dataset]; ok {
			check = "[x]"
			label = "configured"
		}
		fmt.Fprintf(b, "  %s %s %-40s %s\n", marker, check, dataset, label)
	}
	if m.loadingDatasets {
		b.WriteString("\n  Refreshing dataset list…\n")
	} else if m.discoveredDatasets == nil {
		b.WriteString("\n  Press r to discover other datasets the credential can see.\n")
	}
}
