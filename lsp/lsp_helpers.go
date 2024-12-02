package lsp

import (
	"strings"

	"go.lsp.dev/protocol"
)

func GetRangeFromFile(text string, locationRange protocol.Range) string {
	lines := strings.Split(text, "\n")

	var snippetLines []string

	start := locationRange.Start
	end := locationRange.End

	snippetLines = append(snippetLines, lines[start.Line][start.Character:])

	for i := start.Line + 1; i < end.Line; i++ {
		snippetLines = append(snippetLines, lines[i])
	}

	snippetLines = append(snippetLines, lines[end.Line][:end.Character])

	return strings.Join(snippetLines, "\n")
}
