package main

import (
	"fmt"
	"regexp"
)

func main() {
	rx := regexp.MustCompile(`(?i)(NBC|ABC|CBS|FOX|PBS|CW|Telemundo|Univision|Ion)`)
	fmt.Println("WCBS-TV:", rx.MatchString("WCBS-TV"))
	fmt.Println("nbc:", rx.MatchString("nbc"))
}
