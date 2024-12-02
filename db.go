package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	// sqlite_vec "github.com/asg017/sqlite-vec/bindings/go/cgo"
	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// NOTE; when we enable sqlite-vec again, we'll need to re-enable these LDFLAGS
// To do so, remove the /ignore/
// /ignore/ #cgo LDFLAGS: -L${SRCDIR}/third_party/lib -Wl,-undefined,dynamic_lookup
// import "C"

func serializeFloat32(vector []float32) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, vector)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type Execer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

type DB struct {
	Execer
	db *sql.DB
}

type DBTX struct {
	DB
	Execer
	tx *sql.Tx
}

func openDB(wd string) (*DB, error) {
	// sqlite_vec.Auto()
	configDir := getDbsDir()

	dbDir := filepath.Join(configDir, wd)
	dbPath := filepath.Join(dbDir, "index.db")
	err := os.MkdirAll(dbDir, 0755)
	if err != nil {
		return nil, fmt.Errorf("Error creating directory %s: %w", dbDir, err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("Error opening db %s: %w", dbPath, err)
	}

	var sqliteVersion, vecVersion string
	// err = db.QueryRow("select sqlite_version(), vec_version()").Scan(&sqliteVersion, &vecVersion)
	vecVersion = "sqlite-vec disabled until needed"
	err = db.QueryRow("select sqlite_version()").Scan(&sqliteVersion)
	if err != nil {
		return nil, err
	}

	log.Debug().Str("sqlite_version", sqliteVersion).Str("vec_version", vecVersion).Msg("Initialized DB")

	result := &DB{
		Execer: db,
		db:     db,
	}

	return result, result.Init()
}

func (db *DB) Close() error {
	return db.db.Close()
}

func (db *DB) Begin() (*DBTX, error) {
	tx, err := db.db.BeginTx(context.TODO(), &sql.TxOptions{})
	if err != nil {
		return nil, err
	}

	return &DBTX{
		DB: DB{
			Execer: tx,
			db:     db.db,
		},

		Execer: tx,
		tx:     tx,
	}, nil
}

func (dbtx *DBTX) Rollback() error {
	return dbtx.tx.Rollback()
}

func (dbtx *DBTX) Commit() error {
	return dbtx.tx.Commit()
}

func (db *DB) Init() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS file (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL,
			md5 TEXT NOT NULL
		);`)
	if err != nil {
		return fmt.Errorf("Error creating file table: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS symbol (
			id INTEGER PRIMARY KEY,
			kind REAL NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			start_line INTEGER NOT NULL,
			start_col INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			end_col INTEGER NOT NULL,
			file_id INTEGER NOT NULL,
			FOREIGN KEY(file_id) REFERENCES file(id)
		);`)
	if err != nil {
		return fmt.Errorf("Error creating symbol table: %w", err)
	}

	// TODO: handle different vector dimension sizes
	// XXX: commenting out until we have use for embeddings
	// _, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS symbol_embedding USING vec0(embedding float[768]);`)
	// if err != nil {
	// 	return fmt.Errorf("Error creating symbol embeddings table: %w", err)
	// }

	// 	_, err = db.Exec("CREATE VIRTUAL TABLE IF NOT EXISTS symbol_fts USING fts5(name, content=symbol, content_rowid=id);")
	// 	if err != nil {
	// 		return fmt.Errorf("Error creating symbol fts table: %w", err)
	// 	}

	// 	_, err = db.Exec(`
	// CREATE TRIGGER IF NOT EXISTS symbol_ai AFTER INSERT ON symbol BEGIN
	//   INSERT INTO symbol_fts(rowid, name) VALUES (new.id, new.name);
	// END;
	// CREATE TRIGGER IF NOT EXISTS symbol_ad AFTER DELETE ON symbol BEGIN
	//   INSERT INTO symbol_fts(fts_idx, rowid, name) VALUES('delete', old.id, old.name);
	// END;
	// CREATE TRIGGER IF NOT EXISTS symbol_au AFTER UPDATE ON symbol BEGIN
	//   INSERT INTO symbol_fts(fts_idx, rowid, name) VALUES('delete', old.id, old.name);
	//   INSERT INTO symbol_fts(rowid, name) VALUES (new.id, new.name);
	// END;
	// `)
	// 	if err != nil {
	// 		return fmt.Errorf("Error creating symbol : %w", err)
	// 	}

	return nil
}

func (db *DB) InsertFile(path, md5 string) (int64, error) {
	result, err := db.Exec("INSERT INTO file (path, md5) VALUES (?, ?) RETURNING id;", path, md5)
	if err != nil {
		return 0, err
	}

	return result.LastInsertId()
}

func (db *DB) DeleteFileInfo(id int64) error {
	_, err := db.Exec("DELETE FROM symbol WHERE file_id = ?;", id)
	if err != nil {
		return err
	}

	_, err = db.Exec("DELETE FROM file WHERE id = ?;", id)
	return err
}

func (db *DB) DeleteFileInfoByPath(path string) error {
	idRows, err := db.Query("SELECT id FROM file WHERE path = ?;", path)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	for idRows.Next() {
		var fileId int64
		err = idRows.Scan(&fileId)
		if err != nil {
			return err
		}

		_, err = db.Exec("DELETE FROM symbol WHERE file_id = ?", fileId)
		if err != nil {
			return err
		}
	}
	if err := idRows.Err(); err != nil {
		return err
	}

	_, err = db.Exec("DELETE FROM file WHERE path = ?;", path)
	return err
}

func (db *DB) GetFile(path, md5 string) (int64, bool, error) {
	result, err := db.Query("SELECT id FROM file WHERE path = ? AND md5 = ?;", path, md5)
	if err != nil {
		return 0, false, err
	}
	defer result.Close()

	var id int64
	// We know there's at least one row
	ok := result.Next()
	if !ok {
		return 0, false, nil
	}
	err = result.Scan(&id)
	if err != nil {
		return 0, false, err
	}

	return id, true, nil
}

func (db *DB) float64ArrayToFloat32(embedding []float64) []float32 {
	// XXX: this might be super bad. There's an open issue to support f64 which we should track
	//
	var embedding32 []float32 = make([]float32, len(embedding))
	for i, v := range embedding {
		embedding32[i] = float32(v)
	}

	return embedding32
}

func (db *DB) InsertSymbol(fileId int64, kind float64, name, path string, startL, startC, endL, endC int, embedding []float64) (int64, error) {
	serialized, err := serializeFloat32(db.float64ArrayToFloat32(embedding))
	if err != nil {
		return 0, err
	}

	result, err := db.Exec(
		"INSERT INTO symbol (file_id, kind, name, path, start_line, start_col, end_line, end_col) VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id;",
		fileId, kind, name, path, startL, startC, endL, endC,
	)
	if err != nil {
		return 0, err
	}

	symbolId, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	if embedding != nil {
		_, err = db.Exec("INSERT INTO symbol_embedding (rowid, embedding) VALUES (?, ?);", symbolId, serialized)
		if err != nil {
			return 0, err
		}
	}

	return symbolId, nil
}

func (db *DB) FindSymbolByEmbedding(embedding []float64) ([]protocol.SymbolInformation, error) {
	serialized, err := serializeFloat32(db.float64ArrayToFloat32(embedding))
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(`SELECT kind, name, path, start_line, start_col, end_line, end_col FROM symbol WHERE id IN (SELECT rowid FROM symbol_embedding WHERE embedding MATCH ? ORDER BY distance LIMIT 10);`, serialized)
	if err != nil {
		sqliteErr, ok := err.(sqlite3.Error)
		if ok {
			fmt.Println(sqliteErr.Code, sqliteErr.ExtendedCode, sqliteErr.ExtendedCode.Error())
		}
		return nil, err
	}

	var syms []protocol.SymbolInformation

	for rows.Next() {
		info, err := db.scanSymbolRow(rows)
		if err != nil {
			return nil, err
		}

		syms = append(syms, *info)
	}

	return syms, nil
}

func (db *DB) scanSymbolRow(rows *sql.Rows) (*protocol.SymbolInformation, error) {
	var name, path string
	var kind float64
	var startLine, startCol, endLine, endCol uint32
	err := rows.Scan(&kind, &name, &path, &startLine, &startCol, &endLine, &endCol)
	if err != nil {
		return nil, err
	}

	return &protocol.SymbolInformation{
		Name: name,
		Kind: protocol.SymbolKind(kind),
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
	}, nil
}

func (db *DB) FindSymbolByPrefix(prefix string) ([]protocol.SymbolInformation, error) {
	rows, err := db.Query(`SELECT kind, name, path, start_line, start_col, end_line, end_col FROM symbol WHERE name LIKE ? LIMIT 100`, fmt.Sprintf("%s%%", prefix))
	if err != nil {
		return nil, err
	}

	result := []protocol.SymbolInformation{}

	for rows.Next() {
		info, err := db.scanSymbolRow(rows)
		if err != nil {
			return nil, err
		}

		result = append(result, *info)
	}

	return result, nil
}
