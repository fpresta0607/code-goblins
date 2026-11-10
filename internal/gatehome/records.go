package gatehome

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// edit is a file pointRecords changed, with what it held before, for a
// failed move to put back.
type edit struct {
	path     string
	original []byte
}

// pointRecords points what the moved home records of its own place at
// target: every text value in its database, its config.yaml, and the hooks
// path each gated repository pins in its config. A repository a move by hand
// put on the Dev Drive is the old home's as well until the switch, and its
// new hooks path is as good for the old home as for the new one. It returns
// the files it changed, the database among them.
func pointRecords(ctx context.Context, old, target string) ([]edit, error) {
	var edits []edit
	database, err := pointDatabase(ctx, filepath.Join(target, "state.sqlite"), old, target)
	if database != nil {
		edits = append(edits, *database)
	}
	if err != nil {
		return edits, err
	}
	files := []string{filepath.Join(target, "config.yaml")}
	repositories, err := filepath.Glob(filepath.Join(target, "repos", "*.git"))
	if err != nil {
		return nil, err
	}
	for _, repository := range repositories {
		files = append(files, filepath.Join(repository, "config"), filepath.Join(repository, "config.worktree"))
	}
	for _, path := range files {
		original, err := fsx.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return edits, err
		}
		pointed := repoint(string(original), old, target)
		if pointed == string(original) {
			continue
		}
		if err := fsx.AtomicWriteFile(path, []byte(pointed)); err != nil {
			return edits, err
		}
		edits = append(edits, edit{path: path, original: original})
	}
	return edits, nil
}

// restoreEdits puts back what pointRecords changed in a file the copy did not
// make.
func restoreEdits(edits []edit) error {
	var errs []error
	for _, edit := range edits {
		if _, err := os.Lstat(edit.path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		errs = append(errs, fsx.AtomicWriteFile(edit.path, edit.original))
	}
	return errors.Join(errs...)
}

// pointDatabase rewrites every text value of every table in the database at
// path that names a place in old to name the same place in target, in one
// transaction, and proves none is left. It knows no table of the gate's own:
// a column no-mistakes adds later is carried too. It returns the database's
// file as it was, its log checkpointed into it, once it may have changed it.
func pointDatabase(ctx context.Context, path, old, target string) (*edit, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := sql.Open("sqlite", databaseURL(path, ""))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, err
	}
	original, err := fsx.ReadFile(path)
	if err != nil {
		return nil, err
	}
	before := &edit{path: path, original: original}
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := pointTables(ctx, transaction, old, target); err != nil {
		return nil, errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return before, err
	}
	_, err = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return before, err
}

func pointTables(ctx context.Context, transaction *sql.Tx, old, target string) error {
	tables, err := queryStrings(ctx, transaction, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite!_%' ESCAPE '!'")
	if err != nil {
		return err
	}
	for _, table := range tables {
		columns, err := queryStrings(ctx, transaction, "SELECT name FROM pragma_table_info(?)", table)
		if err != nil {
			return err
		}
		for _, column := range columns {
			if err := pointColumn(ctx, transaction, table, column, old, target); err != nil {
				return fmt.Errorf("%s.%s: %w", table, column, err)
			}
		}
	}
	return nil
}

func pointColumn(ctx context.Context, transaction *sql.Tx, table, column, old, target string) error {
	where, arguments := naming(column, old)
	selectSQL := fmt.Sprintf("SELECT rowid, %s FROM %s WHERE typeof(%s) = 'text' AND (%s)", quote(column), quote(table), quote(column), where)
	rows, err := transaction.QueryContext(ctx, selectSQL, arguments...)
	if err != nil {
		return err
	}
	type change struct {
		rowid int64
		value string
	}
	var changes []change
	for rows.Next() {
		var rowid int64
		var value string
		if err := rows.Scan(&rowid, &value); err != nil {
			return errors.Join(err, rows.Close())
		}
		if pointed := repoint(value, old, target); pointed != value {
			changes = append(changes, change{rowid, pointed})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	update := fmt.Sprintf("UPDATE %s SET %s = ? WHERE rowid = ?", quote(table), quote(column))
	for _, change := range changes {
		if _, err := transaction.ExecContext(ctx, update, change.value, change.rowid); err != nil {
			return err
		}
	}
	rest, err := queryStrings(ctx, transaction, fmt.Sprintf("SELECT %s FROM %s WHERE typeof(%s) = 'text' AND (%s)", quote(column), quote(table), quote(column), where), arguments...)
	if err != nil {
		return err
	}
	for _, value := range rest {
		if repoint(value, old, target) != value {
			return fmt.Errorf("a value still names %s after the rewrite", old)
		}
	}
	return nil
}

// naming is a condition that holds for a value that may name old in any
// spelling repoint knows, and its arguments.
func naming(column, old string) (string, []any) {
	var conditions []string
	var arguments []any
	for _, spelling := range spellings(old) {
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(spelling)
		conditions = append(conditions, quote(column)+" LIKE ? ESCAPE '!'")
		arguments = append(arguments, "%"+escaped+"%")
	}
	return strings.Join(conditions, " OR "), arguments
}

func queryStrings(ctx context.Context, transaction *sql.Tx, query string, arguments ...any) ([]string, error) {
	rows, err := transaction.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// databaseURL is the URL that opens the SQLite database at path with query.
func databaseURL(path, query string) string {
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	return (&url.URL{Scheme: "file", Path: name, RawQuery: query}).String()
}

func quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

// spellings are how a record spells the folder: with Windows separators,
// with forward ones, and with Windows separators escaped as in JSON.
func spellings(folder string) []string {
	folder = filepath.Clean(folder)
	return []string{folder, filepath.ToSlash(folder), strings.ReplaceAll(folder, `\`, `\\`)}
}

// repoint replaces each place in text that names old, or a path below it, in
// any of its spellings and in any case, with the same place in target in the
// same spelling. A name that only begins with old's, such as old-backup, is
// left alone.
func repoint(text, old, target string) string {
	olds, targets := spellings(old), spellings(target)
	for index, spelling := range olds {
		text = replaceFolder(text, spelling, targets[index])
	}
	return text
}

func replaceFolder(text, folder, replacement string) string {
	lower, lowerFolder := asciiLower(text), asciiLower(folder)
	var out strings.Builder
	start := 0
	for {
		found := strings.Index(lower[start:], lowerFolder)
		if found < 0 {
			break
		}
		at, end := start+found, start+found+len(folder)
		if end < len(text) && isNameByte(text[end]) {
			out.WriteString(text[start:end])
			start = end
			continue
		}
		out.WriteString(text[start:at])
		out.WriteString(replacement)
		start = end
	}
	out.WriteString(text[start:])
	return out.String()
}

// asciiLower lowers only ASCII letters, so each byte stays where it was.
func asciiLower(text string) string {
	lowered := []byte(text)
	for index, character := range lowered {
		if character >= 'A' && character <= 'Z' {
			lowered[index] = character + 'a' - 'A'
		}
	}
	return string(lowered)
}

// isNameByte is a byte that continues a folder's name.
func isNameByte(character byte) bool {
	return character == '-' || character == '_' || character == '.' || character >= '0' && character <= '9' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= 0x80
}
