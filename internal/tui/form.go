package tui

import (
	"fmt"
	"strconv"
	"strings"

	"bqrest/internal/config"
	tea "charm.land/bubbletea/v2"
)

type formField struct {
	label  string
	value  string
	cursor int // rune offset within value
}

type formGrant struct {
	connection string
	checked    bool
}

// formState is the guided editor for a single connection or caller. Field
// rows come first; caller forms append a grant checklist row per connection.
type formState struct {
	title  string
	index  int // index into cfg.Connections or cfg.Callers; -1 when adding
	fields []formField
	grants []formGrant
	focus  int
	err    string
}

var connectionFieldLabels = []string{
	"Name",
	"Project ID",
	"Location",
	"Credentials env var",
	"Datasets (comma-separated)",
	"Max bytes billed",
	"Max page rows",
	"Max response bytes",
}

func newConnectionForm(conn *config.Connection, index int) *formState {
	values := []string{"", "", "US", "", "", "", "", ""}
	if conn != nil {
		values = []string{
			conn.Name,
			conn.ProjectID,
			conn.Location,
			conn.CredentialsFileEnv,
			strings.Join(conn.Datasets, ", "),
			strconv.FormatInt(conn.Limits.MaxBytesBilled, 10),
			strconv.Itoa(conn.Limits.MaxPageRows),
			strconv.FormatInt(conn.Limits.MaxResponseBytes, 10),
		}
	}
	title := "ADD CONNECTION"
	if conn != nil {
		title = "EDIT CONNECTION · " + conn.Name
	}
	fields := make([]formField, len(connectionFieldLabels))
	for i, label := range connectionFieldLabels {
		fields[i] = formField{label: label, value: values[i], cursor: len([]rune(values[i]))}
	}
	return &formState{title: title, index: index, fields: fields}
}

func newCallerForm(caller *config.Caller, index int, connections []config.Connection) *formState {
	title := "ADD CONSUMER"
	if caller != nil {
		title = "EDIT CONSUMER · " + caller.Name
	}
	form := &formState{
		title: title,
		index: index,
		fields: []formField{
			{label: "Name"},
			{label: "Token env var"},
		},
	}
	if caller != nil {
		for i, value := range []string{caller.Name, caller.TokenEnv} {
			form.fields[i].value = value
			form.fields[i].cursor = len([]rune(value))
		}
	}
	for _, connection := range connections {
		grant := formGrant{connection: connection.Name}
		if caller != nil {
			grant.checked = contains(caller.AllowedConnections, connection.Name)
		}
		form.grants = append(form.grants, grant)
	}
	return form
}

func (f *formState) rowCount() int {
	return len(f.fields) + len(f.grants)
}

func (f *formState) fieldFocused() bool {
	return f.focus < len(f.fields)
}

func (f *formState) focusedField() *formField {
	if !f.fieldFocused() {
		return nil
	}
	return &f.fields[f.focus]
}

func (f *formField) insert(text string) {
	runes := []rune(f.value)
	f.cursor = clamp(f.cursor, 0, len(runes))
	runes = append(runes[:f.cursor], append([]rune(text), runes[f.cursor:]...)...)
	f.value = string(runes)
	f.cursor += len([]rune(text))
}

func (f *formField) backspace() {
	runes := []rune(f.value)
	if f.cursor <= 0 || f.cursor > len(runes) {
		return
	}
	f.value = string(append(runes[:f.cursor-1], runes[f.cursor:]...))
	f.cursor--
}

func (f *formField) deleteForward() {
	runes := []rune(f.value)
	if f.cursor < 0 || f.cursor >= len(runes) {
		return
	}
	f.value = string(append(runes[:f.cursor], runes[f.cursor+1:]...))
}

func (f *formField) moveCursor(delta int) {
	f.cursor = clamp(f.cursor+delta, 0, len([]rune(f.value)))
}

func (m model) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	form := m.form
	form.err = ""
	key := tea.Key(msg)
	switch msg.String() {
	case "esc":
		m.form = nil
		m.mode = modeBrowse
		m.status = "Edit canceled"
		return m, nil
	case "enter":
		m.applyForm()
		return m, nil
	case "up":
		form.focus = clamp(form.focus-1, 0, form.rowCount()-1)
		return m, nil
	case "down":
		form.focus = clamp(form.focus+1, 0, form.rowCount()-1)
		return m, nil
	case "left":
		if field := form.focusedField(); field != nil {
			field.moveCursor(-1)
		}
		return m, nil
	case "right":
		if field := form.focusedField(); field != nil {
			field.moveCursor(1)
		}
		return m, nil
	case "home", "ctrl+a":
		if field := form.focusedField(); field != nil {
			field.cursor = 0
		}
		return m, nil
	case "end", "ctrl+e":
		if field := form.focusedField(); field != nil {
			field.cursor = len([]rune(field.value))
		}
		return m, nil
	case "backspace":
		if field := form.focusedField(); field != nil {
			field.backspace()
		}
		return m, nil
	case "delete":
		if field := form.focusedField(); field != nil {
			field.deleteForward()
		}
		return m, nil
	}
	if !form.fieldFocused() {
		// Focus is on a grant row: Space toggles it.
		if key.Code == tea.KeySpace {
			form.grants[form.focus-len(form.fields)].checked = !form.grants[form.focus-len(form.fields)].checked
		}
		return m, nil
	}
	if key.Code == tea.KeySpace {
		form.focusedField().insert(" ")
		return m, nil
	}
	if key.Text != "" {
		form.focusedField().insert(key.Text)
	}
	return m, nil
}

func (m *model) applyForm() {
	form := m.form
	var err error
	if m.mode == modeConnectionForm {
		err = m.applyConnectionForm(form)
	} else {
		m.applyCallerForm(form)
	}
	if err != nil {
		form.err = err.Error()
		return
	}
	m.form = nil
	m.mode = modeBrowse
	if err := m.cfg.ValidateStructure(); err != nil {
		m.status = "Applied as draft; fix before saving: " + err.Error()
		return
	}
	m.status = "Applied; press s to review and save"
}

func (m *model) applyConnectionForm(form *formState) error {
	maxBytesBilled, err := strconv.ParseInt(strings.TrimSpace(form.fields[5].value), 10, 64)
	if err != nil || maxBytesBilled <= 0 {
		return fmt.Errorf("Max bytes billed must be a positive whole number")
	}
	maxPageRows, err := strconv.ParseInt(strings.TrimSpace(form.fields[6].value), 10, 64)
	if err != nil || maxPageRows <= 0 || maxPageRows > int64(^uint(0)>>1) {
		return fmt.Errorf("Max page rows must be a positive whole number")
	}
	maxResponseBytes, err := strconv.ParseInt(strings.TrimSpace(form.fields[7].value), 10, 64)
	if err != nil || maxResponseBytes <= 0 {
		return fmt.Errorf("Max response bytes must be a positive whole number")
	}
	connection := config.Connection{
		Name:               strings.TrimSpace(form.fields[0].value),
		ProjectID:          strings.TrimSpace(form.fields[1].value),
		Location:           strings.TrimSpace(form.fields[2].value),
		CredentialsFileEnv: strings.TrimSpace(form.fields[3].value),
		Datasets:           splitCSV(form.fields[4].value),
		Limits: config.QueryLimits{
			MaxBytesBilled:   maxBytesBilled,
			MaxPageRows:      int(maxPageRows),
			MaxResponseBytes: maxResponseBytes,
		},
	}
	if form.index >= 0 && form.index < len(m.cfg.Connections) {
		// Guided editing never touches the resource allowlist: preserve it.
		connection.Resources = m.cfg.Connections[form.index].Resources
		m.cfg.Connections[form.index] = connection
		m.connectionIndex = form.index
	} else {
		m.cfg.Connections = append(m.cfg.Connections, connection)
		m.connectionIndex = len(m.cfg.Connections) - 1
	}
	m.catalog = nil
	m.discoveredDatasets = nil
	m.datasetIndex = 0
	return nil
}

func (m *model) applyCallerForm(form *formState) {
	caller := config.Caller{
		Name:     strings.TrimSpace(form.fields[0].value),
		TokenEnv: strings.TrimSpace(form.fields[1].value),
	}
	for _, grant := range form.grants {
		if grant.checked {
			caller.AllowedConnections = append(caller.AllowedConnections, grant.connection)
		}
	}
	if form.index >= 0 && form.index < len(m.cfg.Callers) {
		m.cfg.Callers[form.index] = caller
		m.callerIndex = form.index
	} else {
		m.cfg.Callers = append(m.cfg.Callers, caller)
		m.callerIndex = len(m.cfg.Callers) - 1
	}
}

func splitCSV(value string) []string {
	var result []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// deleteConnection removes the connection, its allowlist, and every consumer
// grant that references it.
func deleteConnection(index int) func(*model) {
	return func(m *model) {
		name := m.cfg.Connections[index].Name
		m.cfg.Connections = append(m.cfg.Connections[:index], m.cfg.Connections[index+1:]...)
		for i := range m.cfg.Callers {
			caller := &m.cfg.Callers[i]
			for j, grant := range caller.AllowedConnections {
				if grant == name {
					caller.AllowedConnections = append(caller.AllowedConnections[:j], caller.AllowedConnections[j+1:]...)
					break
				}
			}
		}
		m.connectionIndex = clamp(m.connectionIndex, 0, max(0, len(m.cfg.Connections)-1))
		m.datasetIndex = 0
		m.catalog = nil
		m.discoveredDatasets = nil
		m.status = fmt.Sprintf("Connection %q deleted; press s to review and save", name)
	}
}

func deleteCaller(index int) func(*model) {
	return func(m *model) {
		name := m.cfg.Callers[index].Name
		m.cfg.Callers = append(m.cfg.Callers[:index], m.cfg.Callers[index+1:]...)
		m.callerIndex = clamp(m.callerIndex, 0, max(0, len(m.cfg.Callers)-1))
		m.status = fmt.Sprintf("Consumer %q deleted; press s to review and save", name)
	}
}

func (m model) viewForm(b *strings.Builder) {
	form := m.form
	fmt.Fprintf(b, "%s\n\n", form.title)
	for i, field := range form.fields {
		marker := " "
		if i == form.focus {
			marker = ">"
		}
		value := field.value
		if i == form.focus {
			runes := []rune(field.value)
			cursor := clamp(field.cursor, 0, len(runes))
			value = string(runes[:cursor]) + "▌" + string(runes[cursor:])
		}
		fmt.Fprintf(b, "  %s %-30s %s\n", marker, field.label+":", value)
	}
	if len(form.grants) > 0 {
		b.WriteString("\n  Connection grants:\n")
		for i, grant := range form.grants {
			row := len(form.fields) + i
			marker := " "
			if row == form.focus {
				marker = ">"
			}
			check := "[ ]"
			if grant.checked {
				check = "[x]"
			}
			fmt.Fprintf(b, "  %s %s %s\n", marker, check, grant.connection)
		}
	}
	if form.err != "" {
		fmt.Fprintf(b, "\n  %s\n", form.err)
	}
	if len(form.grants) == 0 && m.mode == modeCallerForm && len(m.cfg.Connections) == 0 {
		b.WriteString("\n  No connections exist yet; add one in the Connections view.\n")
	}
}
