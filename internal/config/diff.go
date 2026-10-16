package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Marshal renders cfg exactly as WriteAtomic persists it, so previews and
// diffs always match the bytes that land on disk.
func Marshal(cfg Config) ([]byte, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode configuration: %w", err)
	}
	return append(data, '\n'), nil
}

// RenderRedacted returns the persisted JSON form of cfg with any
// sensitive-looking JSON string values masked. Reference fields (names ending
// in _env) stay readable: they hold environment variable names, not secrets.
func RenderRedacted(cfg Config) (string, error) {
	data, err := Marshal(cfg)
	if err != nil {
		return "", err
	}
	return strings.Join(redactLines(data), "\n"), nil
}

// sensitiveValuePattern matches `"<key>": "<value>"` pairs whose key suggests
// a secret. The schema carries only references today, but admin views and diff
// previews pass every rendered line through this filter so a future field
// cannot leak a value into the terminal.
var sensitiveValuePattern = regexp.MustCompile(`("(?:[^"]*(?:secret|token|password|credential|private[_-]?key)[^"]*)")(\s*:\s*)"[^"]*"`)

func redactLine(line string) string {
	return sensitiveValuePattern.ReplaceAllStringFunc(line, func(match string) string {
		sub := sensitiveValuePattern.FindStringSubmatch(match)
		key := strings.Trim(sub[1], `"`)
		if strings.HasSuffix(strings.ToLower(key), "_env") {
			return match
		}
		return fmt.Sprintf("%s%s%q", sub[1], sub[2], "•••")
	})
}

func redactLines(data []byte) []string {
	raw := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i := range raw {
		raw[i] = redactLine(raw[i])
	}
	return raw
}

// Diff renders a unified-style diff between the persisted form of before and
// after, collapsed to changed regions with context lines. All values pass
// through the redaction filter, so the output is safe to display. An empty
// string means the two configurations serialize identically.
func Diff(before, after Config) (string, error) {
	oldData, err := Marshal(before)
	if err != nil {
		return "", err
	}
	newData, err := Marshal(after)
	if err != nil {
		return "", err
	}
	if bytes.Equal(oldData, newData) {
		return "", nil
	}
	ops := diffLines(redactLines(oldData), redactLines(newData))
	return renderHunks(ops, 3), nil
}

type diffOp struct {
	kind byte // ' ' equal, '-' removed, '+' added
	text string
}

// diffLines computes a minimal edit script between two line slices with a
// longest-common-subsequence pass. Config files are small, so the O(n*m) table
// is acceptable and keeps dependencies out.
func diffLines(oldLines, newLines []string) []diffOp {
	n, m := len(oldLines), len(newLines)
	// length[i][j] = LCS length of oldLines[i:] and newLines[j:].
	length := make([][]int, n+1)
	for i := range length {
		length[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				length[i][j] = length[i+1][j+1] + 1
			} else {
				length[i][j] = max(length[i+1][j], length[i][j+1])
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case oldLines[i] == newLines[j]:
			ops = append(ops, diffOp{' ', oldLines[i]})
			i++
			j++
		case length[i+1][j] >= length[i][j+1]:
			ops = append(ops, diffOp{'-', oldLines[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', newLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', oldLines[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', newLines[j]})
	}
	return ops
}

func renderHunks(ops []diffOp, context int) string {
	// Mark which ops are visible: every change plus `context` equal lines
	// around it.
	visible := make([]bool, len(ops))
	for i, op := range ops {
		if op.kind == ' ' {
			continue
		}
		for j := max(0, i-context); j <= min(len(ops)-1, i+context); j++ {
			visible[j] = true
		}
	}
	var b strings.Builder
	b.WriteString("--- on disk\n+++ pending\n")
	hidden := 0
	for i, op := range ops {
		if !visible[i] {
			hidden++
			continue
		}
		if hidden > 0 {
			fmt.Fprintf(&b, "  ⋯ %d unchanged line(s)\n", hidden)
			hidden = 0
		}
		fmt.Fprintf(&b, "%c %s\n", op.kind, op.text)
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "  ⋯ %d unchanged line(s)\n", hidden)
	}
	return b.String()
}
