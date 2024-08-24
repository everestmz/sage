package liveconf

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type ConfigLoader func(data []byte, config any) error

type ConfigWatcher[T any] struct {
	filePath    string
	config      *T
	mutex       sync.RWMutex
	lastModTime time.Time
	loader      ConfigLoader

	errOnNotExist bool
}

type ConfigOption string

const (
	ErrOnNotExist ConfigOption = "Error when file does not exist"
)

func NewConfigWatcher[T any](filePath string, config *T, loader ConfigLoader, options ...ConfigOption) (*ConfigWatcher[T], error) {
	filePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, err
	}

	cw := &ConfigWatcher[T]{
		filePath: filePath,
		config:   config,
		loader:   loader,
	}

	for _, opt := range options {
		switch opt {
		case ErrOnNotExist:
			cw.errOnNotExist = true
		}
	}

	if err := cw.loadConfig(); err != nil {
		return nil, fmt.Errorf("failed to load initial config: %w", err)
	}

	return cw, nil
}

func (cw *ConfigWatcher[T]) Set(config T) {
	cw.config = &config
}

func (cw *ConfigWatcher[T]) Get() (T, error) {
	cw.mutex.Lock()
	defer cw.mutex.Unlock()

	if err := cw.checkAndReload(); err != nil {
		return *cw.config, err
	}

	return *cw.config, nil
}

func (cw *ConfigWatcher[T]) checkAndReload() error {
	stat, err := os.Stat(cw.filePath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	if stat.ModTime() != cw.lastModTime {
		if err := cw.loadConfig(); err != nil {
			return fmt.Errorf("failed to reload config: %w", err)
		}
		cw.lastModTime = stat.ModTime()
	}

	return nil
}

func (cw *ConfigWatcher[T]) loadConfig() error {
	data, err := os.ReadFile(cw.filePath)

	if err != nil {
		if os.IsNotExist(err) && !cw.errOnNotExist {
			// There's no configuration
			return nil
		}

		return fmt.Errorf("failed to read file: %w", err)
	}

	if err := cw.loader(data, cw.config); err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}

	return nil
}
