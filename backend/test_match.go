package main
import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"streamapp/backend/internal/models"
	"streamapp/backend/internal/parser"
)

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Num  int    `json:"channel_number"`
}

func main() {
	resp, err := http.Get("http://192.168.4.143:8080/api/channels")
	if err != nil { return }
	defer resp.Body.Close()
	var channels []Channel
	json.NewDecoder(resp.Body).Decode(&channels)

	channelMap := make(map[string]string)
	numberMap := make(map[string]string)
	for _, c := range channels {
		channelMap[strings.ToLower(c.Name)] = c.ID
		if c.Num > 0 {
			numberMap[fmt.Sprintf("%d", c.Num)] = c.ID
		}
	}

	f, _ := os.Open("../test_epg.gz")
	defer f.Close()
	
	matchedCount := 0
	err = parser.ParseXMLTV(f, func(prog models.EPGProgram, xmlChan *parser.XMLTVChannel) error {
		matchedChanID := ""
		if xmlChan != nil {
			for _, dn := range xmlChan.DisplayName {
				dnLower := strings.ToLower(dn)
				if id, exists := channelMap[dnLower]; exists {
					matchedChanID = id
					break
				}
				for num, id := range numberMap {
					if strings.HasPrefix(dnLower, num+".") || dnLower == num {
						matchedChanID = id
						break
					}
				}
				if matchedChanID != "" { break }
			}
		}
		if matchedChanID != "" {
			matchedCount++
		}
		return nil
	})
	fmt.Println("Total DB Matched Programmes:", matchedCount)
}
