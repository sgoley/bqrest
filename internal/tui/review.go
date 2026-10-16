package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// reviewState backs the pre-save review screen: a redacted diff of the pending
// config against the file on disk, plus a full rendered view of what would be
// written.
type reviewState struct {
	diff     string
	full     string
	showFull bool
	scroll   int
}

type confirmState struct {
	prompt string
	apply  func(*model)
}

func (m model) updateReview(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	lines := m.reviewLines()
	maxScroll := max(0, len(lines)-m.reviewPageSize())
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "n", "esc":
		m.review = nil
		m.mode = modeBrowse
		m.status = "Save canceled; draft changes kept"
	case "y", "enter":
		m.confirmSave()
	case "tab", "v":
		m.review.showFull = !m.review.showFull
		m.review.scroll = 0
	case "up":
		m.review.scroll = max(0, m.review.scroll-1)
	case "down":
		m.review.scroll = min(maxScroll, m.review.scroll+1)
	case "pgup":
		m.review.scroll = max(0, m.review.scroll-m.reviewPageSize())
	case "pgdown":
		m.review.scroll = min(maxScroll, m.review.scroll+m.reviewPageSize())
	case "home":
		m.review.scroll = 0
	case "end":
		m.review.scroll = maxScroll
	}
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		apply := m.confirm.apply
		m.confirm = nil
		m.mode = modeBrowse
		apply(&m)
	case "n", "esc":
		m.confirm = nil
		m.mode = modeBrowse
		m.status = "Canceled"
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

func (m model) reviewLines() []string {
	content := m.review.diff
	title := "REVIEW CHANGES · diff against on-disk config"
	if m.review.showFull {
		content = m.review.full
		title = "REVIEW CHANGES · pending config (redacted)"
	}
	return append([]string{title}, strings.Split(strings.TrimRight(content, "\n"), "\n")...)
}

// reviewPageSize estimates how many content lines fit above the status/footer
// chrome; falls back to a sane default before the first WindowSizeMsg.
func (m model) reviewPageSize() int {
	if m.height <= 0 {
		return 15
	}
	return max(5, m.height-10)
}

func (m model) viewReview(b *strings.Builder) {
	lines := m.reviewLines()
	page := m.reviewPageSize()
	start := clamp(m.review.scroll, 0, max(0, len(lines)-1))
	end := min(len(lines), start+page)
	for _, line := range lines[start:end] {
		fmt.Fprintf(b, "%s\n", line)
	}
	if len(lines) > page {
		fmt.Fprintf(b, "\n  line %d-%d of %d\n", start+1, end, len(lines))
	}
	b.WriteString("\n  Draft only — nothing is written until you confirm. Secrets are never part of the config.\n")
}
