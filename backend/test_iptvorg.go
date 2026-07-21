package main

import (
	"fmt"
	"net/http"
)

func main() {
	resp, err := http.Get("https://iptv-org.github.io/iptv/countries/us.m3u")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("Status:", resp.StatusCode)
}
