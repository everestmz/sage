package main

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestApplyChangesToDocument(t *testing.T) {
	tests := []struct {
		name           string
		initialDoc     string
		changes        []protocol.TextDocumentContentChangeEvent
		expectedResult string
		expectError    bool
	}{
		{
			name:       "Single line addition",
			initialDoc: "Hello\nWorld\n",
			changes: []protocol.TextDocumentContentChangeEvent{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 5},
						End:   protocol.Position{Line: 0, Character: 5},
					},
					Text: " there",
				},
			},
			expectedResult: "Hello there\nWorld\n",
			expectError:    false,
		},
		{
			name:       "New line addition",
			initialDoc: "Hello\nWorld\n",
			changes: []protocol.TextDocumentContentChangeEvent{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 0},
						End:   protocol.Position{Line: 1, Character: 0},
					},
					Text: "\n",
				},
			},
			expectedResult: "Hello\n\nWorld\n",
			expectError:    false,
		},
		{
			name:       "Multi-line change",
			initialDoc: "Line 1\nLine 2\nLine 3\n",
			changes: []protocol.TextDocumentContentChangeEvent{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 6},
						End:   protocol.Position{Line: 2, Character: 0},
					},
					Text: " modified\nNew",
				},
			},
			expectedResult: "Line 1 modified\nNewLine 3\n",
			expectError:    false,
		},
		{
			name:       "Out of bounds change",
			initialDoc: "Single line",
			changes: []protocol.TextDocumentContentChangeEvent{
				{
					Range: protocol.Range{
						Start: protocol.Position{Line: 1, Character: 0},
						End:   protocol.Position{Line: 1, Character: 0},
					},
					Text: "This should fail",
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyChangesToDocument(tt.initialDoc, tt.changes)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tt.expectedResult {
					t.Errorf("Expected result %q, but got %q", tt.expectedResult, result)
				}
			}
		})
	}
}
