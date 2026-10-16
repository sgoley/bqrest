package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

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
	switch m.mode {
	case modeColumns:
		m.viewColumns(&b)
	case modeDatasets:
		m.viewDatasetPicker(&b)
	case modeConnectionForm, modeCallerForm:
		m.viewForm(&b)
	case modeReview:
		m.viewReview(&b)
	case modeConfirm:
		fmt.Fprintf(&b, "  %s\n\n  y confirm   n/esc cancel\n", m.confirm.prompt)
	default:
		switch m.tab {
		case 0:
			m.viewConnections(&b)
		case 1:
			m.viewCallers(&b)
		case 2:
			m.viewResources(&b)
		}
	}
	if m.status != "" {
		fmt.Fprintf(&b, "\n  %s\n", m.status)
	}
	b.WriteString("\n────────────────────────────────────────────────────────────────────────────\n")
	b.WriteString(m.footer())
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

func (m model) footer() string {
	switch m.mode {
	case modeColumns:
		return "  ↑/↓ select  Space toggle column  Enter/Esc done  q quit\n"
	case modeDatasets:
		return "  ↑/↓ select  Space configure/unconfigure  Enter browse dataset  r discover  Esc back  q quit\n"
	case modeConnectionForm, modeCallerForm:
		return "  ↑/↓ field  ←/→ cursor  type to edit  Space toggles grants  Enter apply  Esc cancel\n"
	case modeReview:
		return "  ↑/↓ PgUp/PgDn scroll  Tab switch diff/config  y/Enter save  n/Esc cancel\n"
	case modeConfirm:
		return "  y confirm   n/Esc cancel\n"
	}
	switch m.tab {
	case 0:
		return "  ↑/↓ select  a add  e edit  x delete  s review & save  1–3/tab navigate  q quit\n"
	case 1:
		return "  ↑/↓ select  a add  e edit  x delete  s review & save  1–3/tab navigate  q quit\n"
	default:
		return "  c connection  d datasets  r refresh  ↑/↓ select  Space expose/hide  Enter columns  s review & save  q quit\n"
	}
}

func (m model) viewConnections(b *strings.Builder) {
	b.WriteString("CONNECTIONS\n\n")
	if len(m.cfg.Connections) == 0 {
		b.WriteString("  No connections configured. Press a to add one.\n")
		return
	}
	for i, connection := range m.cfg.Connections {
		marker := " "
		if i == m.connectionIndex {
			marker = ">"
		}
		fmt.Fprintf(b, "  %s %s\n", marker, connection.Name)
		fmt.Fprintf(b, "    BigQuery project: %s    Location: %s\n", connection.ProjectID, connection.Location)
		fmt.Fprintf(b, "    Datasets: %s\n", strings.Join(connection.Datasets, ", "))
		fmt.Fprintf(b, "    Credential reference: %s (%s)\n", connection.CredentialsFileEnv, m.credentialStatus(connection.CredentialsFileEnv))
		fmt.Fprintf(b, "    Limits: %d rows/page, %d response bytes, %d bytes billed\n\n", connection.Limits.MaxPageRows, connection.Limits.MaxResponseBytes, connection.Limits.MaxBytesBilled)
	}
}

func (m model) viewCallers(b *strings.Builder) {
	b.WriteString("CONSUMERS & GRANTS\n\n")
	if len(m.cfg.Callers) == 0 {
		b.WriteString("  No consumers configured. Press a to add one.\n")
		return
	}
	for i, caller := range m.cfg.Callers {
		marker := " "
		if i == m.callerIndex {
			marker = ">"
		}
		fmt.Fprintf(b, "  %s %s\n", marker, caller.Name)
		fmt.Fprintf(b, "    Token reference: %s (%s)\n", caller.TokenEnv, m.envStatus(caller.TokenEnv))
		fmt.Fprintf(b, "    Granted connections: %s\n\n", strings.Join(caller.AllowedConnections, ", "))
	}
	b.WriteString("  Secret values are never displayed.\n")
}

func (m model) viewResources(b *strings.Builder) {
	b.WriteString("TABLE & COLUMN EXPOSURE\n\n")
	if !m.hasSelection() {
		b.WriteString("  No connection or dataset is configured.\n  Press d to pick and discover datasets once a connection exists.\n")
		return
	}
	connection := m.selectedConnection()
	dataset := m.selectedDataset()
	fmt.Fprintf(b, "  Connection: %s   Project: %s   Dataset: %s\n", connection.Name, connection.ProjectID, dataset)
	fmt.Fprintf(b, "  Credential reference: %s (%s)\n\n", connection.CredentialsFileEnv, m.credentialStatus(connection.CredentialsFileEnv))
	if m.loadingCatalog {
		b.WriteString("  Loading tables and views from BigQuery…\n")
		return
	}
	if m.catalog == nil {
		b.WriteString("  Press r to load available tables and views, d to manage datasets.\n")
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
