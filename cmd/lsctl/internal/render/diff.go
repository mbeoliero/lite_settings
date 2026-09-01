package render

import (
	"fmt"
	"slices"
	"strings"
)

// DiffContext is the default number of context lines in a unified diff.
const DiffContext = 3

// maxDiffCells bounds LCS's O(n*m) time and space; larger inputs use delete/add.
const maxDiffCells = 4 << 20

// UnifiedDiff produces a unified diff with ctxLines of context on each
// side, or the empty string when the two sides match.
func UnifiedDiff(oldText, newText, oldLabel, newLabel string, ctxLines int) string {
	hs := hunks(diffLines(splitLines(oldText), splitLines(newText)), ctxLines)
	// Hunk presence ignores trailing-newline-only differences without empty headers.
	if len(hs) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", oldLabel, newLabel)
	for _, h := range hs {
		sb.WriteString(h)
	}
	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type op struct {
	kind byte // ' ' same, '-' removed, '+' added
	text string
}

func diffLines(a, b []string) []op {
	// Trim shared edges so typical edits need a small LCS table.
	var head, tail []op
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		head = append(head, op{' ', a[0]})
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		tail = append(tail, op{' ', a[len(a)-1]})
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	slices.Reverse(tail) // collected back to front

	var mid []op
	switch {
	case len(a) == 0 && len(b) == 0:
	case len(a) == 0:
		mid = mark('+', b)
	case len(b) == 0:
		mid = mark('-', a)
	case len(a)*len(b) > maxDiffCells:
		mid = append(mark('-', a), mark('+', b)...)
	default:
		mid = lcsDiff(a, b)
	}

	out := make([]op, 0, len(head)+len(mid)+len(tail))
	out = append(out, head...)
	out = append(out, mid...)
	return append(out, tail...)
}

func mark(kind byte, lines []string) []op {
	out := make([]op, len(lines))
	for i, l := range lines {
		out[i] = op{kind, l}
	}
	return out
}

// lcsDiff uses classic LCS for a small, dependency-free line diff.
func lcsDiff(a, b []string) []op {
	n, m := len(a), len(b)
	// int32 suffices: maxDiffCells keeps the counts far below 2^31.
	tbl := make([]int32, (n+1)*(m+1))
	at := func(i, j int) int32 { return tbl[i*(m+1)+j] }

	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			var v int32
			if a[i] == b[j] {
				v = at(i+1, j+1) + 1
			} else if at(i+1, j) >= at(i, j+1) {
				v = at(i+1, j)
			} else {
				v = at(i, j+1)
			}
			tbl[i*(m+1)+j] = v
		}
	}

	var out []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, op{' ', a[i]})
			i, j = i+1, j+1
		case at(i+1, j) >= at(i, j+1):
			// Preserve conventional delete-before-add order.
			out = append(out, op{'-', a[i]})
			i++
		default:
			out = append(out, op{'+', b[j]})
			j++
		}
	}
	out = append(out, mark('-', a[i:])...)
	return append(out, mark('+', b[j:])...)
}

func hunks(ops []op, ctxLines int) []string {
	ctxLines = max(ctxLines, 0)

	keep := make([]bool, len(ops))
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		for k := max(0, i-ctxLines); k <= min(len(ops)-1, i+ctxLines); k++ {
			keep[k] = true
		}
	}

	var out []string
	aLine, bLine := 1, 1
	for i := 0; i < len(ops); {
		if !keep[i] {
			switch ops[i].kind {
			case ' ':
				aLine, bLine = aLine+1, bLine+1
			case '-':
				aLine++
			case '+':
				bLine++
			}
			i++
			continue
		}

		start, aStart, bStart := i, aLine, bLine
		var aCount, bCount int
		for i < len(ops) && keep[i] {
			switch ops[i].kind {
			case ' ':
				aCount, bCount = aCount+1, bCount+1
			case '-':
				aCount++
			case '+':
				bCount++
			}
			i++
		}
		aLine, bLine = aLine+aCount, bLine+bCount

		var sb strings.Builder
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", span(aStart, aCount), span(bStart, bCount))
		for _, o := range ops[start:i] {
			sb.WriteByte(o.kind)
			sb.WriteString(o.text)
			sb.WriteByte('\n')
		}
		out = append(out, sb.String())
	}
	return out
}

// span uses unified-diff's backed-up "start,0" for empty ranges.
func span(start, count int) string {
	if count == 0 {
		return fmt.Sprintf("%d,0", start-1)
	}
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
