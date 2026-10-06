package mcpinstall

import (
	"fmt"
	"strings"
)

// diffContext is the number of unchanged lines shown around a change.
const diffContext = 3

// maxLCSCells bounds the LCS table. Lines common to both ends are trimmed
// first, so a config edit leaves a few lines in the middle; past the bound
// the middle is shown as removed and added whole.
const maxLCSCells = 1 << 20

type diffOp struct {
	kind byte // ' ', '-' or '+'
	text string
}

// UnifiedDiff is a line-based unified diff from a to b, with diffContext
// lines of context, or "" when they are equal. Line endings are not shown.
func UnifiedDiff(oldLabel, newLabel string, a, b []byte) string {
	al, bl := splitLines(string(a)), splitLines(string(b))
	ops := diffLines(al, bl)
	changed := false
	for _, op := range ops {
		if op.kind != ' ' {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", oldLabel, newLabel)
	// Line numbers before each op.
	oldNo := make([]int, len(ops)+1)
	newNo := make([]int, len(ops)+1)
	for i, op := range ops {
		oldNo[i+1], newNo[i+1] = oldNo[i], newNo[i]
		if op.kind != '+' {
			oldNo[i+1]++
		}
		if op.kind != '-' {
			newNo[i+1]++
		}
	}
	for i := 0; i < len(ops); {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		// A hunk: from this change, extended while the next change is
		// within 2*diffContext unchanged lines.
		start := max(0, i-diffContext)
		end := i
		for j := i; j < len(ops); j++ {
			if ops[j].kind != ' ' {
				end = j
			} else if j-end > 2*diffContext {
				break
			}
		}
		stop := min(len(ops), end+diffContext+1)
		oldCount := oldNo[stop] - oldNo[start]
		newCount := newNo[stop] - newNo[start]
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", hunkRange(oldNo[start], oldCount), hunkRange(newNo[start], newCount))
		for _, op := range ops[start:stop] {
			sb.WriteByte(op.kind)
			sb.WriteString(op.text)
			sb.WriteByte('\n')
		}
		i = stop
	}
	return sb.String()
}

// hunkRange formats "start,count" with 1-based start; an empty range
// names the line before it.
func hunkRange(before, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", before)
	}
	return fmt.Sprintf("%d,%d", before+1, count)
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// diffLines returns the edit script from a to b (longest common
// subsequence on the lines between the common prefix and suffix).
func diffLines(a, b []string) []diffOp {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, l := range a[:p] {
		ops = append(ops, diffOp{' ', l})
	}
	ma, mb := a[p:len(a)-s], b[p:len(b)-s]
	if n, m := len(ma), len(mb); n > 0 && m > 0 && (n+1)*(m+1) <= maxLCSCells {
		ops = append(ops, lcsOps(ma, mb)...)
	} else {
		for _, l := range ma {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range mb {
			ops = append(ops, diffOp{'+', l})
		}
	}
	for _, l := range a[len(a)-s:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// t[i][j] = LCS length of a[i:] and b[j:].
	t := make([]int32, (n+1)*(m+1))
	at := func(i, j int) int32 { return t[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			v := max(at(i+1, j), at(i, j+1))
			if a[i] == b[j] {
				v = at(i+1, j+1) + 1
			}
			t[i*(m+1)+j] = v
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case at(i+1, j) >= at(i, j+1):
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}
