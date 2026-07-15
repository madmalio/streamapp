package parser

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"streamapp/backend/internal/models"
)

// HDHomeRunDevice is the object returned by SiliconDust's cloud discovery service.
type HDHomeRunDevice struct {
	DeviceID    string `json:"DeviceID"`
	LocalIP     string `json:"LocalIP"`
	DiscoverURL string `json:"DiscoverURL"`
	LineupURL   string `json:"LineupURL"`
}

// HDHomeRunChannel represents a channel stream configuration in tuner lineups.
type HDHomeRunChannel struct {
	GuideNumber string `json:"GuideNumber"`
	GuideName   string `json:"GuideName"`
	URL         string `json:"URL"`
	HD          int    `json:"HD,omitempty"`
}

// DiscoverHDHomeRun queries the SiliconDust local discovery API to identify active tuners.
func DiscoverHDHomeRun() ([]HDHomeRunDevice, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://ipv4.api.hdhomerun.com/discover")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to discover devices: status %d", resp.StatusCode)
	}

	var devices []HDHomeRunDevice
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return nil, err
	}
	return devices, nil
}

// FetchHDHomeRunChannels queries tuner lineup.json by device IP or URL.
func FetchHDHomeRunChannels(ipOrLineupURL string) ([]models.Channel, error) {
	lineupURL := ipOrLineupURL
	if !strings.HasPrefix(ipOrLineupURL, "http://") && !strings.HasPrefix(ipOrLineupURL, "https://") {
		lineupURL = fmt.Sprintf("http://%s/lineup.json", ipOrLineupURL)
	} else if !strings.HasSuffix(ipOrLineupURL, "/lineup.json") && !strings.Contains(ipOrLineupURL, "lineup.json") {
		ipOrLineupURL = strings.TrimSuffix(ipOrLineupURL, "/")
		lineupURL = ipOrLineupURL + "/lineup.json"
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(lineupURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch lineup: status %d", resp.StatusCode)
	}

	var lineup []HDHomeRunChannel
	if err := json.NewDecoder(resp.Body).Decode(&lineup); err != nil {
		return nil, err
	}

	var channels []models.Channel
	for _, hc := range lineup {
		chno := 0
		parts := strings.Split(hc.GuideNumber, ".")
		if len(parts) > 0 {
			var parsed int
			if _, err := fmt.Sscanf(parts[0], "%d", &parsed); err == nil {
				chno = parsed
			}
		}

		channels = append(channels, models.Channel{
			ID:            uuid.New().String(),
			Name:          hc.GuideName,
			StreamURL:     hc.URL,
			LogoURL:       "", // HDHomeRun line-ups do not specify logo images
			ChannelNumber: chno,
		})
	}

	return channels, nil
}
