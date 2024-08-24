package main

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

type ContextItemGetterFunc func(ContextApi) (*ContextItem, error)

type ContextItemType string

const (
	ContextItemTypeFile   ContextItemType = "file"
	ContextItemTypeSymbol ContextItemType = "symbol"
	ContextItemTypeRange  ContextItemType = "range"
)

type ContextApi interface {
	GetSymbol(filename string, symbolName string) (string, error)
	GetRange(filename string, start, end int) (string, error)
	GetFile(filename string) (string, error)
}

type ContextItem struct {
	Filename   string
	Identifier string // Line number range or symbol name
	Content    string
}

type ContextItemProvider struct {
	parts    []string
	itemType ContextItemType
	getter   ContextItemGetterFunc
}

func (cip *ContextItemProvider) Type() ContextItemType {
	return cip.itemType
}

func (cip *ContextItemProvider) String() string {
	return strings.Join(cip.parts, " ")
}

func (cip *ContextItemProvider) GetItem(api ContextApi) (*ContextItem, error) {
	return cip.getter(api)
}

func ParseContext(contextDefinition string) ([]*ContextItemProvider, error) {
	scanner := bufio.NewScanner(strings.NewReader(contextDefinition))
	scanner.Split(bufio.ScanLines)

	var providers []*ContextItemProvider

	for scanner.Scan() {
		line := scanner.Text()
		newProviders, err := parseContextLine(line)
		if err != nil {
			return nil, fmt.Errorf("Error for line '%s': %w", line, err)
		}

		providers = append(providers, newProviders...)
	}

	return providers, nil
}

func getLineParts(line string) ([]string, error) {
	var parts []string

	var currentPart strings.Builder

	var inQuotes bool
	var escaped bool

	for _, char := range line {
		switch {
		case escaped:
			currentPart.WriteRune(char)
			escaped = false
		case char == '\\':
			escaped = true
		case char == '"' && !escaped:
			inQuotes = !inQuotes

			if !inQuotes {
				parts = append(parts, currentPart.String())
				currentPart.Reset()
			}

		case char == ' ' && !inQuotes:
			if currentPart.Len() > 0 {
				parts = append(parts, currentPart.String())
				currentPart.Reset()
			}
		default:
			currentPart.WriteRune(char)
		}
	}

	if currentPart.Len() > 0 {
		parts = append(parts, currentPart.String())
	}

	if inQuotes {
		return nil, fmt.Errorf("Found quote with no matching closing quote")
	}

	return parts, nil
}

func parseRange(rangeStr string) (start, end int, err error) {
	parts := strings.Split(rangeStr, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("Found range with >2 parts: '%s'", rangeStr)
	}

	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("Invalid start of range '%s': %w", parts[0], err)
	}

	end, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("Invalid end of range '%s': %w", parts[1], err)
	}

	if start > end {
		return 0, 0, fmt.Errorf("Start cannot be greater than end of range '%s'", rangeStr)
	}

	return
}

func parseContextLine(line string) ([]*ContextItemProvider, error) {
	if line == "" {
		return nil, nil
	}

	parts, err := getLineParts(line)
	if err != nil {
		return nil, err
	}

	var providers []*ContextItemProvider

	filename := parts[0]

	// Our options right now are a whole file, a file range, or a symbol.
	// Each row can have one filename, but multiple options for symbols or line ranges
	if len(parts) == 1 {
		providers = append(providers, &ContextItemProvider{
			parts:    parts,
			itemType: ContextItemTypeFile,
			getter: func(ca ContextApi) (*ContextItem, error) {
				content, err := ca.GetFile(filename)
				if err != nil {
					return nil, err
				}

				return &ContextItem{
					Filename:   filename,
					Identifier: "",
					Content:    content,
				}, nil
			},
		})

		return providers, nil
	}

	// We have more than one item for this file

	for _, item := range parts[1:] {
		itemType := ContextItemTypeSymbol
		var getItem ContextItemGetterFunc = func(ca ContextApi) (*ContextItem, error) {
			content, err := ca.GetSymbol(filename, item)
			if err != nil {
				return nil, err
			}

			return &ContextItem{
				Filename:   filename,
				Identifier: item,
				Content:    content,
			}, nil
		}

		if strings.Contains(item, ":") {
			itemType = ContextItemTypeRange
			start, end, err := parseRange(item)
			if err != nil {
				return nil, err
			}

			getItem = func(ca ContextApi) (*ContextItem, error) {
				content, err := ca.GetRange(filename, start, end)
				if err != nil {
					return nil, err
				}

				return &ContextItem{
					Filename:   filename,
					Identifier: fmt.Sprintf("%d:%d", start, end),
					Content:    content,
				}, nil
			}
		}

		providers = append(providers, &ContextItemProvider{
			parts:    parts,
			itemType: itemType,
			getter:   getItem,
		})
	}

	return providers, nil
}

func BuildContext(providers []*ContextItemProvider, api ContextApi) (string, error) {
	var builder strings.Builder

	for _, provider := range providers {
		var tagName string

		contextItem, err := provider.GetItem(api)
		if err != nil {
			return "", err
		}

		tagParams := map[string]string{
			"file": contextItem.Filename,
		}

		switch provider.Type() {
		case ContextItemTypeFile:
			tagName = "File"
		case ContextItemTypeRange:
			tagName = "FileRange"
			tagParams["range"] = contextItem.Identifier
		case ContextItemTypeSymbol:
			tagName = "FileSymbol"
			tagParams["symbol"] = contextItem.Identifier
		default:
			return "", fmt.Errorf("Invalid item type '%s'", provider.Type())
		}

		builder.WriteString("<" + tagName + "\n")
		for k, v := range tagParams {
			_, err = builder.WriteString(fmt.Sprintf("%s=\"%s\"\n", k, v))
			if err != nil {
				return "", err
			}
		}
		builder.WriteString(">\n")
		builder.WriteString(contextItem.Content)
		builder.WriteString("\n")
		builder.WriteString("</" + tagName + ">\n")
	}

	return builder.String(), nil
}
