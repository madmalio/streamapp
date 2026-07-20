
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "database.sqlite?_foreign_keys=on")
	if err != nil {
		fmt.Println("open", err)
		return
	}
	tx, _ := db.Begin()
	_, err = tx.Exec("DELETE FROM epg_programs")
	if err != nil {
		fmt.Println("delete", err)
	}
	err = tx.Commit()
	if err != nil {
		fmt.Println("commit", err)
	} else {
		fmt.Println("success")
	}
}

