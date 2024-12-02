package lsp

import (
	"strings"

	"go.lsp.dev/protocol"
)

func TreeSymKindToLspKind(kind string) protocol.SymbolKind {
	switch kind {
	case "class":
		return protocol.SymbolKindClass
	case "function":
		return protocol.SymbolKindFunction
	case "method":
		return protocol.SymbolKindMethod
	case "type":
		// NOTE: this is what typescript-language-server calls a typedef. /shrug
		return protocol.SymbolKindVariable
	case "module":
		return protocol.SymbolKindModule
	case "interface":
		return protocol.SymbolKindInterface
	case "enum":
		return protocol.SymbolKindEnum
	case "macro":
		// XXX: this is definitely wrong
		fallthrough
	default:
		return protocol.SymbolKindVariable
	}
}

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
