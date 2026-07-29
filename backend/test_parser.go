package main

import (
	"fmt"
	"streamapp/backend/internal/parser"
)

func main() {
	fmt.Println("nbc:", parser.SmartCategorize("nbc", ""))
	fmt.Println("abc:", parser.SmartCategorize("abc", ""))
	fmt.Println("cbs:", parser.SmartCategorize("cbs", ""))
	fmt.Println("WCBS-TV:", parser.SmartCategorize("WCBS-TV", ""))
	fmt.Println("MeTV:", parser.SmartCategorize("MeTV", ""))
}
