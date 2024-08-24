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

type SagePathConfig struct {
	Path      *string                       `yaml:"path"`
	Languages map[string]SageLanguageConfig `yaml:"languages"`
	Include   []string                      `yaml:"include"`
	Exclude   []string                      `yaml:"exclude"`
	Models    *liveconf.ConfigWatcher[SageModelsConfig]
	Context   *liveconf.ConfigWatcher[[]*ContextItemProvider]

	compiledIncludes []glob.Glob
	compiledExcludes []glob.Glob
	name             string
}

func (sc SagePathConfig) Name() string {
	return sc.name
}

func (sc *SagePathConfig) GetLanguageConfigForFile(relativePath string) (string, *SageLanguageConfig) {
	pathExt := filepath.Ext(relativePath)

	for name, config := range sc.Languages {
		for _, ext := range config.Extensions {
			if pathExt == ext {
				return name, &config
			}
		}
	}

	return "", nil
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

		if config.Path != nil {
			expandedPath := os.ExpandEnv(*config.Path)
			if !filepath.IsAbs(expandedPath) {
				return nil, fmt.Errorf("filepath '%s' for config '%s' is not absolute", expandedPath, name)
			}
			*config.Path = expandedPath
		}

		for _, pattern := range config.Exclude {
			compiled, err := glob.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("'%s.exclude': '%s' is invalid: %w", name, pattern, err)
			}
			config.compiledExcludes = append(config.compiledExcludes, compiled)
		}

		for _, pattern := range config.Include {
			compiled, err := glob.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("'%s.include': '%s' is invalid: %w", name, pattern, err)
			}
			config.compiledIncludes = append(config.compiledIncludes, compiled)
		}

		modelsConfig := SageModelsConfig{}
		config.Models, err = liveconf.NewConfigWatcher[SageModelsConfig](getWorkspaceModelsPath(), &modelsConfig, yaml.Unmarshal)
		if err != nil {
			return nil, err
		}

		providers := []*ContextItemProvider{}
		config.Context, err = liveconf.NewConfigWatcher[[]*ContextItemProvider](getWorkspaceContextPath(), &providers, func(data []byte, config any) error {
			providersPtr := config.(*[]*ContextItemProvider)
			providers, err := ParseContext(string(data))
			if err != nil {
				return err
			}

			*providersPtr = providers
			return nil
		})
		if err != nil {
			return nil, err
		}

		modelsConfig, err = config.Models.Get()
		if err != nil {
			return nil, err
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

		config.Models.Set(modelsConfig)

		if len(config.Languages) == 0 {
			return nil, fmt.Errorf("configuration '%s' needs at least one language configuration", name)
		}

		for langName, langConfig := range config.Languages {
			if len(langConfig.Extensions) == 0 {
				return nil, fmt.Errorf("configuration '%s.languages.%s' needs at least one file extension", name, langName)
			}

			if langConfig.LanguageServer.Command == nil {
				return nil, fmt.Errorf("configuration '%s.languages.%s.command' cannot be empty", name, langName)
			}
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
		return nil, fmt.Errorf("Create a config file in ~/.config/sage/config.yaml")
	}
	if err != nil {
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
		return nil, fmt.Errorf("No configuration found for working directory %s", wd)
	}

	if err != nil {
		return nil, err
	}

	return config, nil
}
