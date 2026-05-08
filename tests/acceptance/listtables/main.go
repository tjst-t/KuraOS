// listtables prints SQLite table names one per line. It exists so
// tests/acceptance/*.sh can verify schema without depending on the sqlite3 CLI
// being installed on the developer host (CGo-free, just like the main binary).
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: listtables <db-path>")
		os.Exit(2)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(ON)", os.Args[1])
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(n)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
