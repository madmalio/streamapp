package parser

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
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
	DeviceAuth  string `json:"DeviceAuth"`
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
			GroupID:       SmartCategorize(hc.GuideName, ""),
			ChannelNumber: chno,
			GuideNumber:   hc.GuideNumber,
		})
	}

	return channels, nil
}

// FetchHDHomeRunEPG discovers a local tuner, extracts its DeviceAuth, and downloads the XMLTV from SiliconDust.
func FetchHDHomeRunEPG(ipOrDiscoverURL string, callback func(prog models.EPGProgram, xmlChan *XMLTVChannel) error) error {
	var discoverURL string
	if ipOrDiscoverURL == "" {
		devices, err := DiscoverHDHomeRun()
		if err != nil {
			return fmt.Errorf("failed to discover HDHomeRun: %w", err)
		}
		if len(devices) == 0 {
			return fmt.Errorf("no HDHomeRun devices found on network")
		}
		discoverURL = devices[0].DiscoverURL
	} else if strings.HasSuffix(ipOrDiscoverURL, "/discover.json") {
		discoverURL = ipOrDiscoverURL
	} else {
		ip := strings.TrimPrefix(strings.TrimPrefix(ipOrDiscoverURL, "http://"), "https://")
		ip = strings.TrimSuffix(ip, "/")
		discoverURL = fmt.Sprintf("http://%s/discover.json", ip)
	}

	// Step 1: Hit the local discover.json for the first tuner to get DeviceAuth
	client := &http.Client{Timeout: 5 * time.Second}
	respLocal, err := client.Get(discoverURL)
	if err != nil {
		return fmt.Errorf("failed to contact local device %s: %w", discoverURL, err)
	}
	defer respLocal.Body.Close()

	var localDev HDHomeRunDevice
	if err := json.NewDecoder(respLocal.Body).Decode(&localDev); err != nil {
		return fmt.Errorf("failed to decode local discover.json: %w", err)
	}

	if localDev.DeviceAuth == "" {
		return fmt.Errorf("no DeviceAuth found from local device (ensure tuner is connected to internet)")
	}

	// Step 2: Fetch the XMLTV guide from SiliconDust cloud API
	epgURL := fmt.Sprintf("https://api.hdhomerun.com/api/xmltv?DeviceAuth=%s", localDev.DeviceAuth)
	req, err := http.NewRequest("GET", epgURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create EPG request: %w", err)
	}
	// The SiliconDust XMLTV is large and typically gzipped
	req.Header.Set("Accept-Encoding", "gzip")
	// SiliconDust blocks default Go-http-client with 403 Forbidden
	req.Header.Set("User-Agent", "StreamApp/1.0 (Mozilla/5.0)")

	// Use a longer timeout for downloading the large guide file
	clientEPG := &http.Client{Timeout: 60 * time.Second}
	respEPG, err := clientEPG.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download EPG from SiliconDust: %w", err)
	}
	defer respEPG.Body.Close()

	if respEPG.StatusCode != http.StatusOK {
		return fmt.Errorf("SiliconDust EPG API returned status: %d", respEPG.StatusCode)
	}

	// Step 3: Decompress if necessary
	var reader io.Reader = respEPG.Body
	if respEPG.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(respEPG.Body)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	
	return ParseXMLTV(reader, callback)
}
