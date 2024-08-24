package main

import (
	"reflect"
	"testing"
)

func TestParseContext(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []*ContextItemProvider
		wantErr bool
	}{
		{
			name:  "Single file",
			input: "file.go",
			want: []*ContextItemProvider{
				{
					parts:    []string{"file.go"},
					itemType: ContextItemTypeFile,
				},
			},
			wantErr: false,
		},
		{
			name:  "File with symbol",
			input: "file.go MyFunction",
			want: []*ContextItemProvider{
				{
					parts:    []string{"file.go", "MyFunction"},
					itemType: ContextItemTypeSymbol,
				},
			},
			wantErr: false,
		},
		{
			name:  "File with range",
			input: "file.go 10:20",
			want: []*ContextItemProvider{
				{
					parts:    []string{"file.go", "10:20"},
					itemType: ContextItemTypeRange,
				},
			},
			wantErr: false,
		},
		{
			name:  "File with multiple items",
			input: "file.go MyFunction 10:20 AnotherSymbol",
			want: []*ContextItemProvider{
				{
					parts:    []string{"file.go", "MyFunction", "10:20", "AnotherSymbol"},
					itemType: ContextItemTypeSymbol,
				},
				{
					parts:    []string{"file.go", "MyFunction", "10:20", "AnotherSymbol"},
					itemType: ContextItemTypeRange,
				},
				{
					parts:    []string{"file.go", "MyFunction", "10:20", "AnotherSymbol"},
					itemType: ContextItemTypeSymbol,
				},
			},
			wantErr: false,
		},
		{
			name:  "Multiple lines",
			input: "file1.go MyFunction\nfile2.go 10:20",
			want: []*ContextItemProvider{
				{
					parts:    []string{"file1.go", "MyFunction"},
					itemType: ContextItemTypeSymbol,
				},
				{
					parts:    []string{"file2.go", "10:20"},
					itemType: ContextItemTypeRange,
				},
			},
			wantErr: false,
		},
		{
			name:    "Invalid range",
			input:   "file.go 20:10",
			want:    nil,
			wantErr: true,
		},
		{
			name:  "Quoted filename with spaces",
			input: `"my file.go" MyFunction`,
			want: []*ContextItemProvider{
				{
					parts:    []string{"my file.go", "MyFunction"},
					itemType: ContextItemTypeSymbol,
				},
			},
			wantErr: false,
		},
		{
			name:    "Unclosed quote",
			input:   `"my file.go MyFunction`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseContext(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseContext() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			for i, gotItem := range got {
				if i >= len(tt.want) {
					t.Errorf("ParseContext() = %v, want %v", got, tt.want)
				}
				wantItem := tt.want[i]

				if gotItem.itemType != wantItem.itemType {
					t.Errorf("Got item %v, want %v", gotItem, wantItem)
				}

				if len(gotItem.parts) != len(wantItem.parts) {
					t.Errorf("Got item %v, want %v", gotItem, wantItem)
				}

				for p, gotPart := range gotItem.parts {
					if gotPart != wantItem.parts[p] {
						t.Errorf("Got item %v, want %v", gotItem, wantItem)
					}
				}
			}
		})
	}
}

func TestContextItemProvider_GetItem(t *testing.T) {
	// Mock ContextApi implementation
	mockApi := &MockContextApi{}

	tests := []struct {
		name     string
		provider *ContextItemProvider
		want     *ContextItem
		wantErr  bool
	}{
		{
			name: "Get file",
			provider: &ContextItemProvider{
				parts:    []string{"file.go"},
				itemType: ContextItemTypeFile,
				getter: func(ca ContextApi) (*ContextItem, error) {
					content, _ := ca.GetFile("file.go")
					return &ContextItem{Filename: "file.go", Content: content}, nil
				},
			},
			want:    &ContextItem{Filename: "file.go", Content: "file content"},
			wantErr: false,
		},
		{
			name: "Get symbol",
			provider: &ContextItemProvider{
				parts:    []string{"file.go", "MyFunction"},
				itemType: ContextItemTypeSymbol,
				getter: func(ca ContextApi) (*ContextItem, error) {
					content, _ := ca.GetSymbol("file.go", "MyFunction")
					return &ContextItem{Filename: "file.go", Identifier: "MyFunction", Content: content}, nil
				},
			},
			want:    &ContextItem{Filename: "file.go", Identifier: "MyFunction", Content: "function content"},
			wantErr: false,
		},
		{
			name: "Get range",
			provider: &ContextItemProvider{
				parts:    []string{"file.go", "10:20"},
				itemType: ContextItemTypeRange,
				getter: func(ca ContextApi) (*ContextItem, error) {
					content, _ := ca.GetRange("file.go", 10, 20)
					return &ContextItem{Filename: "file.go", Identifier: "10:20", Content: content}, nil
				},
			},
			want:    &ContextItem{Filename: "file.go", Identifier: "10:20", Content: "range content"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.provider.GetItem(mockApi)
			if (err != nil) != tt.wantErr {
				t.Errorf("ContextItemProvider.GetItem() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ContextItemProvider.GetItem() = %v, want %v", got, tt.want)
			}
		})
	}
}

// MockContextApi is a mock implementation of ContextApi for testing
type MockContextApi struct{}

func (m *MockContextApi) GetSymbol(filename, symbolName string) (string, error) {
	return "function content", nil
}

func (m *MockContextApi) GetRange(filename string, start, end int) (string, error) {
	return "range content", nil
}

func (m *MockContextApi) GetFile(filename string) (string, error) {
	return "file content", nil
}
