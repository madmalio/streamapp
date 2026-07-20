package main
import (
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
)
func main() {
	db, err := sql.Open("sqlite3", "database.sqlite?_foreign_keys=on")
	if err != nil {
		fmt.Println("open err:", err)
		return
	}
	defer db.Close()

	_, err = db.Exec("INSERT INTO epg_programs (id, channel_id, title, start_time, end_time) VALUES ('test_prog', '9ac25091-b9cc-453d-8c8b-a6b1812e0378', 'Test', '2020', '2021')")
	if err != nil {
		fmt.Println("insert err:", err)
	} else {
		fmt.Println("success")
	}
}
