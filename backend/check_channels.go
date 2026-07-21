package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "streamapp.db")
	if err != nil {
		fmt.Println("Open error:", err)
		return
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM channels").Scan(&count)
	if err != nil {
		fmt.Println("Query error:", err)
		return
	}
	fmt.Println("Total channels:", count)
}
