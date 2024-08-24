package main

type LanguageServerConfig struct {
	Command string         `json:"command"`
	Args    []string       `json:"args"`
	Config  map[string]any `json:"config"`
}

type SageLanguageServerConfig struct {
	LanguageServers []*LanguageServerConfig `json:"language_servers"`
}

// Sort of acts as a main for this part of the binary
func StartLanguageServer() error {
	return nil
}
