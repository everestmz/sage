package locality

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

var languageQueries = map[Language]string{
	"go": `(call_expression
	function: (selector_expression
		field: (field_identifier) @field_function))

(call_expression
    function: (identifier) @function)

(composite_literal
    type: (qualified_type
      package: (package_identifier)
      name: (type_identifier) @struct_name))

(selector_expression
    field: (field_identifier) @field_name)`,
}

type Language string

func (l Language) TS() *sitter.Language {
	switch l {
	case "go":
		return golang.GetLanguage()
	case "py":
		return python.GetLanguage()
	case "ts":
		return typescript.GetLanguage()
	case "tsx":
		fallthrough
	case "jsx":
		return tsx.GetLanguage()
	case "js":
		return javascript.GetLanguage()
	default:
		panic("No language set up for " + l)
	}
}

func GetLanguage(fileName string) Language {
	return Language(strings.TrimPrefix(filepath.Ext(fileName), "."))
}

func GetParser(language *sitter.Language) *sitter.Parser {
	parser := sitter.NewParser()
	parser.SetLanguage(language)

	return parser
}

func New(lsp protocol.Server) *Locality {
	l := &Locality{
		Lsp:       lsp,
		listeners: map[string]chan string{},
	}

	go func() {
		err := l.Serve()
		if err != nil {
			panic(err)
		}
	}()

	return l
}

type Locality struct {
	Lsp protocol.Server

	listeners map[string]chan string
}

type Context struct {
	Captures    map[string][]*CodeNode `json:"captures"`
	Definitions map[string]string      `json:"definitions"`
	File        string                 `json:"file"`
	Line        int                    `json:"line"`
	Queries     string                 `json:"queries"`
}

func (l *Locality) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Error().Err(err).Msg("Bad connection")
		return
	}
	defer c.CloseNow()

	id := uuid.NewString()
	l.listeners[id] = make(chan string)

	defer delete(l.listeners, id)

	// Do we need to check c.Subprotocol?
	for newCtxJson := range l.listeners[id] {
		// Whenever we get a ping here, check the current state
		err = c.Write(context.TODO(), websocket.MessageText, []byte(newCtxJson))
		if err != nil {
			log.Error().Err(err).Msg("Bad connection")
			return
		}
	}

}

func (l *Locality) Serve() error {
	err := http.ListenAndServe(":13579", l)
	return err
}

func (l *Locality) sendUpdate(newCtx string) {
	for _, listener := range l.listeners {
		listener <- newCtx
	}
}

type Position struct {
	Line   uint32 `json:"line"`
	Column uint32 `json:"column"`
}

type CodeNode struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Start   Position `json:"start"`
	End     Position `json:"end"`
	Type    string   `json:"type"`
}

func (l *Locality) GetContext(fileName, content string, line int) (*Context, error) {
	language := GetLanguage(fileName)
	parser := GetParser(language.TS())
	source := []byte(content)

	tree, err := parser.ParseCtx(context.TODO(), nil, source)
	if err != nil {
		return nil, err
	}

	queries, ok := languageQueries[language]
	if !ok {
		return nil, fmt.Errorf("No language queries for %s", language)
	}

	q, err := sitter.NewQuery([]byte(queries), language.TS())
	if err != nil {
		return nil, err
	}

	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	codeCtx := &Context{
		Captures:    map[string][]*CodeNode{},
		Definitions: map[string]string{},
		File:        content,
		Line:        line,
		Queries:     queries,
	}

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		m = qc.FilterPredicates(m, source)
		for _, c := range m.Captures {
			// XXX: We also may want to jump to typedef sometimes, not just def
			locations, err := l.Lsp.Definition(context.TODO(), &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{
						URI: uri.File(fileName),
					},
					Position: protocol.Position{
						Line:      c.Node.StartPoint().Row,
						Character: c.Node.StartPoint().Column,
					},
				},
			})
			if err != nil {
				// XXX: for now, just ignore errors
				continue
			}

			for _, location := range locations {
				uniqueId := fmt.Sprintf("%s %d:%d-%d:%d", location.URI.Filename(), location.Range.Start.Line, location.Range.Start.Character, location.Range.End.Line, location.Range.End.Character)

				codeCtx.Captures[uniqueId] = append(codeCtx.Captures[uniqueId], &CodeNode{
					ID:      uniqueId,
					Content: c.Node.Content(source),
					Start: Position{
						Line:   c.Node.StartPoint().Row,
						Column: c.Node.StartPoint().Column,
					},
					End: Position{
						Line:   c.Node.EndPoint().Row,
						Column: c.Node.EndPoint().Column,
					},
					Type: c.Node.Type(),
				})

				// XXX: HACK: we shouldn't be reading files in this library, we should take some way of being
				// provided a file. Can do later.
				fileContents, err := os.ReadFile(location.URI.Filename())
				if err != nil {
					return nil, err
				}
				// lsp.GetRangeFromFile(string(fileContents), location.Range)

				definitionFileTree, err := parser.ParseCtx(context.TODO(), nil, fileContents)
				if err != nil {
					return nil, err
				}

				definitionNode := definitionFileTree.RootNode().NamedDescendantForPointRange(
					sitter.Point{
						Row:    location.Range.Start.Line,
						Column: location.Range.Start.Character,
					},
					sitter.Point{
						Row:    location.Range.End.Line,
						Column: location.Range.End.Character,
					},
				)

				for definitionNode.Parent() != nil && definitionNode.Parent().Parent() != nil {
					definitionNode = definitionNode.Parent()
				}

				codeCtx.Definitions[uniqueId] = definitionNode.Content(fileContents)

			}
		}
	}

	bs, err := json.Marshal(codeCtx)
	if err != nil {
		return nil, err
	}

	l.sendUpdate(string(bs))

	return codeCtx, nil
}
