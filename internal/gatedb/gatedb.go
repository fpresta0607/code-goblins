// Package gatedb answers the fleet's reads of the gate's state database, the
// SQLite database no-mistakes keeps, in process.
//
// Each read started sqlite3: 1,259 of the 4,327 programs the board started in
// 95 minutes on 2026-11-03 were sqlite3 (#1610), and every program start costs
// a process creation and a Microsoft Defender scan. Here the database is read
// with the SQLite no-mistakes writes it with, modernc.org/sqlite, so a read
// starts no program and answers as sqlite3 did.
package gatedb

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Runner answers each read-only query of a database, the request
// sqlite3 -readonly -json <database> <sql> makes, in process and as sqlite3
// prints it: one JSON object per row in an array, nothing for no rows, and
// exit code 1 with the refusal on stderr. It runs every other request with
// Next, a write to the database among them.
type Runner struct {
	Next execx.Runner
}

func (r Runner) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	args := req.Args
	if req.Name != "sqlite3" || len(args) != 4 || args[0] != "-readonly" || args[1] != "-json" || !filepath.IsAbs(args[2]) {
		return r.Next.Run(ctx, req)
	}
	rows, err := query(ctx, args[2], args[3])
	if ctx.Err() != nil {
		return execx.Result{}, ctx.Err()
	}
	if err != nil {
		return execx.Result{ExitCode: 1, Stderr: []byte("Error: " + err.Error() + "\n")}, nil
	}
	return execx.Result{Stdout: rows}, nil
}

// query runs statement against the database at path, opened read-only for
// this one read and closed after it, so no handle of the board's holds the
// file between reads.
func query(ctx context.Context, path, statement string) ([]byte, error) {
	name := filepath.ToSlash(path)
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: name, RawQuery: "mode=ro"}).String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	keys := make([][]byte, len(columns))
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for index, column := range columns {
		keys[index], _ = json.Marshal(column)
		targets[index] = &values[index]
	}
	var out bytes.Buffer
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		if out.Len() == 0 {
			out.WriteByte('[')
		} else {
			out.WriteString(",\n")
		}
		out.WriteByte('{')
		for index, value := range values {
			if index > 0 {
				out.WriteByte(',')
			}
			// sqlite3 prints a blob's bytes as a string.
			if blob, ok := value.([]byte); ok {
				value = string(blob)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			out.Write(keys[index])
			out.WriteByte(':')
			out.Write(encoded)
		}
		out.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out.Len() > 0 {
		out.WriteString("]\n")
	}
	return out.Bytes(), nil
}
