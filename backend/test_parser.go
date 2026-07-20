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
	matchedChan := 0
	err := parser.ParseXMLTV(f, func(prog models.EPGProgram, xmlChan *parser.XMLTVChannel) error {
		count++
		if xmlChan != nil {
			matchedChan++
			if matchedChan == 1 {
				fmt.Println("First matched channel display names:", xmlChan.DisplayName)
			}
		}
		return nil
	})
	fmt.Printf("Total programmes: %d\nProgrammes with channel mapping: %d\nErr: %v\n", count, matchedChan, err)
}
