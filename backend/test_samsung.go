package main

import (
	"fmt"
	"net/http"
)

func main() {
	resp, err := http.Get("https://i.mjh.nz/SamsungTVPlus/us.m3u8")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("Status:", resp.StatusCode)
}
