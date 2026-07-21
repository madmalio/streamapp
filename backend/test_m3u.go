package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

func main() {
	resp, err := http.Get("https://i.mjh.nz/PlutoTV/us.m3u8")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Println("Status:", resp.StatusCode)
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Println("Body length:", len(body))
}
