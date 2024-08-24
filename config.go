package main

import (
	"os"
	"path/filepath"
)

func getConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	path := filepath.Join(home, ".config", "sage", "dbs")

	err = os.MkdirAll(path, 0755)
	if err != nil {
		panic(err)
	}

	return path
}
