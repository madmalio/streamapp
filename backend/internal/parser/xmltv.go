package parser

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"streamapp/backend/internal/models"
)

// XMLTVChannel represents a channel definition in XMLTV files.
type XMLTVChannel struct {
	ID          string   `xml:"id,attr"`
	DisplayName []string `xml:"display-name"`
	Icon        []struct {
		Src string `xml:"src,attr"`
	} `xml:"icon"`
}

// XMLTVProgramme represents a program listing in XMLTV files.
type XMLTVProgramme struct {
	Channel string `xml:"channel,attr"`
	Start   string `xml:"start,attr"`
	Stop    string `xml:"stop,attr"`
	Title   []struct {
		Text string `xml:",chardata"`
	} `xml:"title"`
	Desc []struct {
		Text string `xml:",chardata"`
	} `xml:"desc"`
}

// ParseXMLTVTime parses various common XMLTV date-time string formats.
func ParseXMLTVTime(val string) (time.Time, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return time.Time{}, fmt.Errorf("empty time string")
	}

	layouts := []string{
		"20060102150405 -0700",
		"20060102150405 -07:00",
		"20060102150405 -07",
		"20060102150405 +0700",
		"20060102150405 +07:00",
		"20060102150405 +07",
		"20060102150405",
		"200601021504 -0700",
		"200601021504 +0700",
		"200601021504",
		"2006010215",
		"20060102",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, val); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown XMLTV time format: %s", val)
}

// ParseXMLTV streams programs from an XMLTV EPG source and calls the callback for each.
func ParseXMLTV(r io.Reader, callback func(prog models.EPGProgram) error) error {
	decoder := xml.NewDecoder(r)

	for {
		t, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		switch se := t.(type) {
		case xml.StartElement:
			if se.Name.Local == "programme" {
				var p XMLTVProgramme
				if err := decoder.DecodeElement(&p, &se); err != nil {
					return err
				}

				startTime, err := ParseXMLTVTime(p.Start)
				if err != nil {
					continue // Skip entry if start time is invalid
				}
				endTime, err := ParseXMLTVTime(p.Stop)
				if err != nil {
					continue // Skip entry if stop time is invalid
				}

				title := ""
				if len(p.Title) > 0 {
					title = p.Title[0].Text
				}
				desc := ""
				if len(p.Desc) > 0 {
					desc = p.Desc[0].Text
				}

				prog := models.EPGProgram{
					ChannelID:   p.Channel, // Stores EPG channel reference ID (e.g. tvg-id)
					Title:       title,
					Description: desc,
					StartTime:   startTime,
					EndTime:     endTime,
				}

				if err := callback(prog); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
