package parser

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"streamapp/backend/internal/models"
)

// ParseM3U parses an M3U playlist from an io.Reader in a memory-efficient way.
func ParseM3U(r io.Reader) ([]models.Channel, error) {
	var channels []models.Channel
	scanner := bufio.NewScanner(r)

	// Set a 512KB buffer capacity to handle very long lines without scanning errors
	const maxCapacity = 512 * 1024
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	var currentName string
	var currentLogo string
	var currentGroup string
	var currentChNo int
	hasExtInf := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXTINF:") {
			currentName, currentLogo, currentGroup, currentChNo = parseExtInf(line)
			hasExtInf = true
			continue
		}

		// Skip other header/comment metadata lines
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Line is a stream URL
		if hasExtInf {
			channel := models.Channel{
				ID:            uuid.New().String(),
				GroupID:       currentGroup,
				Name:          currentName,
				StreamURL:     line,
				LogoURL:       currentLogo,
				ChannelNumber: currentChNo,
			}
			channels = append(channels, channel)

			// Reset temp state
			currentName = ""
			currentLogo = ""
			currentGroup = ""
			currentChNo = 0
			hasExtInf = false
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return channels, nil
}

// parseExtInf parses the metadata from a "#EXTINF" line.
func parseExtInf(line string) (name string, logo string, group string, chno int) {
	content := strings.TrimPrefix(line, "#EXTINF:")

	// Find the comma separating duration & attributes from the channel name
	commaIdx := -1
	inQuotes := false
	for i := 0; i < len(content); i++ {
		if content[i] == '"' {
			inQuotes = !inQuotes
		} else if content[i] == ',' && !inQuotes {
			commaIdx = i
			break
		}
	}

	var attrPart string
	if commaIdx != -1 {
		name = strings.TrimSpace(content[commaIdx+1:])
		attrPart = content[:commaIdx]
	} else {
		attrPart = content
	}

	// Extract attributes
	logo = extractAttribute(attrPart, "tvg-logo")
	group = extractAttribute(attrPart, "group-title")
	chnoStr := extractAttribute(attrPart, "tvg-chno")
	if chnoStr == "" {
		chnoStr = extractAttribute(attrPart, "chno")
	}
	if chnoStr != "" {
		if val, err := strconv.Atoi(chnoStr); err == nil {
			chno = val
		}
	}

	// Fallbacks
	if name == "" {
		name = extractAttribute(attrPart, "tvg-name")
	}
	if group == "" {
		group = extractAttribute(attrPart, "group")
	}

	return name, logo, group, chno
}

// extractAttribute retrieves the value of a key-value attribute (e.g. key="value" or key=value)
// while avoiding false substring matches.
func extractAttribute(s string, key string) string {
	keyEq := key + "="
	remaining := s

	for {
		idx := strings.Index(remaining, keyEq)
		if idx == -1 {
			return ""
		}

		// Ensure it is a complete key (either start of string or preceded by space/tab/comma)
		if idx == 0 || remaining[idx-1] == ' ' || remaining[idx-1] == '\t' || remaining[idx-1] == ',' {
			start := idx + len(keyEq)
			if start >= len(remaining) {
				return ""
			}

			// Value is quoted
			if remaining[start] == '"' {
				start++
				end := strings.IndexByte(remaining[start:], '"')
				if end == -1 {
					return remaining[start:]
				}
				return remaining[start : start+end]
			}

			// Value is unquoted; read until space or end
			end := strings.IndexByte(remaining[start:], ' ')
			if end == -1 {
				return remaining[start:]
			}
			return remaining[start : start+end]
		}

		// Move past this partial match and continue searching
		remaining = remaining[idx+len(keyEq):]
	}
}
