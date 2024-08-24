package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func init() {
	sql.Register("sqlite3_fts5", &sqlite3.SQLiteDriver{
		// Extensions: []string{
		// 	"fts5",
		// },
	})
}

type DB struct {
	*sql.DB
}

func openDB(wd string) (*DB, error) {
	configDir := getConfigDir()

	dbDir := filepath.Join(configDir, wd)
	dbPath := filepath.Join(dbDir, "index.db")
	err := os.MkdirAll(dbDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("Error creating directory %s: %w", dbDir, err)
	}

	db, err := sql.Open("sqlite3_fts5", dbPath)
	if err != nil {
		return nil, fmt.Errorf("Error opening db %s: %w", dbPath, err)
	}

	result := &DB{
		db,
	}

	return result, result.Init()
}

func (db *DB) Init() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS symbol (
			id INTEGER PRIMARY KEY,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			start_col INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			end_col INTEGER NOT NULL
		);`)

	_, err = db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS symbol_fts USING fts5(name, content=symbol, content_rowid=id);")
	if err != nil {
		return err
	}

	_, err = db.Exec(`
CREATE TRIGGER IF NOT EXISTS symbol_ai AFTER INSERT ON symbol BEGIN
  INSERT INTO symbol_fts(rowid, name) VALUES (new.id, new.name);
END;
CREATE TRIGGER IF NOT EXISTS symbol_ad AFTER DELETE ON symbol BEGIN
  INSERT INTO symbol_fts(fts_idx, rowid, name) VALUES('delete', old.id, old.name);
END;
CREATE TRIGGER IF NOT EXISTS symbol_au AFTER UPDATE ON symbol BEGIN
  INSERT INTO symbol_fts(fts_idx, rowid, name) VALUES('delete', old.id, old.name);
  INSERT INTO symbol_fts(rowid, name) VALUES (new.id, new.name);
END;
`)
	if err != nil {
		return err
	}

	return nil
}

func (db *DB) InsertSymbol(kind, name, path string, startL, startC, endL, endC int) (int64, error) {
	result, err := db.Exec(
		"INSERT INTO symbol (kind, name, path, start_line, start_col, end_line, end_col) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id;",
		kind, name, path, startL, startC, endL, endC,
	)

	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (db *DB) FindSymbolByPrefix(prefix string) ([]protocol.SymbolInformation, error) {
	rows, err := db.Query(`SELECT kind, name, path, start_line, start_col, end_line, end_col FROM symbol WHERE name LIKE ? LIMIT 100`, fmt.Sprintf("%s%%", prefix))
	if err != nil {
		return nil, err
	}

	result := []protocol.SymbolInformation{}

	for rows.Next() {
		var kind, name, path string
		var startLine, startCol, endLine, endCol uint32
		err = rows.Scan(&kind, &name, &path, &startLine, &startCol, &endLine, &endCol)
		if err != nil {
			return nil, err
		}

		result = append(result, protocol.SymbolInformation{
			Name: name,
			Kind: 12,
			Location: protocol.Location{
				URI: uri.File(path),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      startLine,
						Character: startCol,
					},
					End: protocol.Position{
						Line:      endLine,
						Character: endCol,
					},
				},
			},
		})
	}

	return result, nil
}
