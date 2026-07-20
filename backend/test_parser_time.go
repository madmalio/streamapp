package main
import (
	"fmt"
	"os"

	"streamapp/backend/internal/models"
	"streamapp/backend/internal/parser"
)

func main() {
	f, _ := os.Open("../test_epg.gz")
	defer f.Close()
	
	count := 0
	err := parser.ParseXMLTV(f, func(prog models.EPGProgram, xmlChan *parser.XMLTVChannel) error {
		count++
		if count == 1 {
			fmt.Println("First program:", prog.Title, prog.StartTime.UTC().Format("2006-01-02T15:04:05Z"))
		}
		if count == 6718 {
			fmt.Println("Last program:", prog.Title, prog.StartTime.UTC().Format("2006-01-02T15:04:05Z"))
		}
		return nil
	})
	fmt.Println("Done parsing.", err)
}
