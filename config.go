package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/everestmz/sage/liveconf"
	"github.com/gobwas/glob"
	"gopkg.in/yaml.v3"
)

type SageLanguageConfig struct {
	Extensions     []string              `yaml:"extensions"`
	LanguageServer *LanguageServerConfig `yaml:"language_server"`
}

type SageModelsConfig struct {
	Embedding   *string `yaml:"embedding,omitempty"`
	Default     *string `yaml:"default,omitempty"`
	ExplainCode *string `yaml:"explain_code,omitempty"`
}

func NewPathConfig(name string) *SagePathConfig {
	return &SagePathConfig{
		Path: nil,
		name: name,
	}
}

type SagePathConfig struct {
	Path    *string  `yaml:"path"`
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	Models  *liveconf.ConfigWatcher[SageModelsConfig]
	Context *liveconf.ConfigWatcher[[]*ContextItemProvider]

	compiledIncludes []glob.Glob
	compiledExcludes []glob.Glob
	name             string
}

func (sc SagePathConfig) Name() string {
	return sc.name
}

func (sc *SagePathConfig) InitDefaults() error {
	if sc.Path != nil {
		expandedPath := os.ExpandEnv(*sc.Path)
		if !filepath.IsAbs(expandedPath) {
			return fmt.Errorf("filepath '%s' for config '%s' is not absolute", expandedPath, sc.name)
		}
		*sc.Path = expandedPath
	}

	for _, pattern := range sc.Exclude {
		compiled, err := glob.Compile(pattern)
		if err != nil {
			return fmt.Errorf("'%s.exclude': '%s' is invalid: %w", sc.name, pattern, err)
		}
		sc.compiledExcludes = append(sc.compiledExcludes, compiled)
	}

	for _, pattern := range sc.Include {
		compiled, err := glob.Compile(pattern)
		if err != nil {
			return fmt.Errorf("'%s.include': '%s' is invalid: %w", sc.name, pattern, err)
		}
		sc.compiledIncludes = append(sc.compiledIncludes, compiled)
	}

	modelsConfig := SageModelsConfig{}
	defaultModelsConfig, err := yaml.Marshal(SageModelsConfig{
		Default:     &DefaultModel,
		ExplainCode: &DefaultExplainCodeModel,
		Embedding:   &DefaultEmbeddingModel,
	})
	if err != nil {
		return err
	}
	sc.Models, err = liveconf.NewConfigWatcher[SageModelsConfig](getWorkspaceModelsPath(), string(defaultModelsConfig), &modelsConfig, yaml.Unmarshal)
	if err != nil {
		return err
	}

	providers := []*ContextItemProvider{}
	sc.Context, err = liveconf.NewConfigWatcher[[]*ContextItemProvider](getWorkspaceContextPath(), "", &providers, func(data []byte, config any) error {
		providersPtr := config.(*[]*ContextItemProvider)
		providers, err := ParseContext(string(data))
		if err != nil {
			return err
		}

		*providersPtr = providers
		return nil
	})
	if err != nil {
		return err
	}

	modelsConfig, err = sc.Models.Get()
	if err != nil {
		return err
	}

	if modelsConfig.Default == nil {
		modelsConfig.Default = &DefaultModel
	}

	if modelsConfig.Embedding == nil {
		modelsConfig.Embedding = &DefaultEmbeddingModel
	}

	if modelsConfig.ExplainCode == nil {
		modelsConfig.ExplainCode = &DefaultExplainCodeModel
	}

	sc.Models.Set(modelsConfig)

	return nil
}

type SageConfig map[string]*SagePathConfig

func (sc SageConfig) GetConfigForDir(wd string) *SagePathConfig {
	baseName := filepath.Base(wd)

	var tentativeConfig *SagePathConfig

	for name, config := range sc {
		if name == baseName {
			tentativeConfig = config
		}

		if config.Path != nil && *config.Path == wd {
			return config
		}
	}

	// Could be nil still
	return tentativeConfig
}

var (
	DefaultModel            = "llama3.1:8b"
	DefaultEmbeddingModel   = "nomic-embed-text"
	DefaultExplainCodeModel = "starcoder2:3b"
)

func getConfigFromFile(path string) (SageConfig, error) {
	bs, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := SageConfig{}

	err = yaml.Unmarshal(bs, &config)
	if err != nil {
		return nil, err
	}

	for name, config := range config {
		config.name = name

		err = config.InitDefaults()
		if err != nil {
			return nil, err
		}
	}

	return config, nil
}

func getWorkspaceDir(projectDir string) string {
	wsDir := filepath.Join(getConfigDir(), "workspaces", projectDir)
	err := os.MkdirAll(wsDir, 0755)
	if err != nil {
		panic(err)
	}

	return wsDir
}

func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	path := filepath.Join(home, ".config", "sage")

	err = os.MkdirAll(path, 0755)
	if err != nil {
		panic(err)
	}

	return path
}

func getDbsDir() string {
	return filepath.Join(getConfigDir(), "dbs")
}

func getConfigFile() (SageConfig, error) {
	configDir := getConfigDir()

	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	wdPath := filepath.Join(wd, "sage.yaml")
	info, err := os.Stat(wdPath)
	if err == nil && !info.IsDir() {
		return getConfigFromFile(wdPath)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	info, err = os.Stat(configPath)
	if os.IsNotExist(err) {
		err = os.MkdirAll(configDir, 0755)
		if err != nil {
			return nil, err
		}
		err = os.WriteFile(configPath, []byte("\n"), 0755)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	return getConfigFromFile(configPath)
}

func getWorkspaceContextPath() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	wsDir := getWorkspaceDir(wd)
	contextFile := filepath.Join(wsDir, "context.txt")

	return contextFile
}

func getWorkspaceModelsPath() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}

	wsDir := getWorkspaceDir(wd)
	modelsConfig := filepath.Join(wsDir, "models.yaml")

	return modelsConfig
}

func getConfigForWd() (*SagePathConfig, error) {
	configs, err := getConfigFile()
	if err != nil {
		return nil, err
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// TODO: we need to support subdirs by mapping back up to the parent wd

	config := configs.GetConfigForDir(wd)
	if config == nil {
		config = NewPathConfig(filepath.Base(wd))
		err = config.InitDefaults()
		if err != nil {
			return nil, err
		}
	}

	if err != nil {
		return nil, err
	}

	return config, nil
}

func getWorkspaceSocketPath(wd string) string {
	wsDir := getWorkspaceDir(wd)

	return filepath.Join(wsDir, "language_server.sock")
}
