package parser

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestParseM3U_Basic(t *testing.T) {
	m3uContent := `#EXTM3U
#EXTINF:-1 tvg-id="CNN.us" tvg-name="CNN" tvg-logo="https://example.com/cnn.png" group-title="News" tvg-chno="10",CNN US
http://cnn-stream.example.com/live.m3u8
#EXTINF:0 group-title="Movies" chno="99",Action Movie Channel
http://movies.example.com/action.m3u8
#EXTINF:-1,Simple Channel No Attributes
http://simple.example.com/live.m3u8
`

	channels, err := ParseM3U(strings.NewReader(m3uContent))
	if err != nil {
		t.Fatalf("ParseM3U returned error: %v", err)
	}

	if len(channels) != 3 {
		t.Errorf("Expected 3 channels, got %d", len(channels))
	}

	// Verify Channel 1
	c1 := channels[0]
	if c1.Name != "CNN US" {
		t.Errorf("Expected name 'CNN US', got '%s'", c1.Name)
	}
	if c1.LogoURL != "https://example.com/cnn.png" {
		t.Errorf("Expected logo 'https://example.com/cnn.png', got '%s'", c1.LogoURL)
	}
	if c1.GroupID != "News" {
		t.Errorf("Expected group 'News', got '%s'", c1.GroupID)
	}
	if c1.ChannelNumber != 10 {
		t.Errorf("Expected channel number 10, got %d", c1.ChannelNumber)
	}
	if c1.StreamURL != "http://cnn-stream.example.com/live.m3u8" {
		t.Errorf("Expected stream URL 'http://cnn-stream.example.com/live.m3u8', got '%s'", c1.StreamURL)
	}
	if c1.ID == "" {
		t.Error("Expected channel ID to be generated, got empty string")
	}

	// Verify Channel 2
	c2 := channels[1]
	if c2.Name != "Action Movie Channel" {
		t.Errorf("Expected name 'Action Movie Channel', got '%s'", c2.Name)
	}
	if c2.GroupID != "Movies" {
		t.Errorf("Expected group 'Movies', got '%s'", c2.GroupID)
	}
	if c2.ChannelNumber != 99 {
		t.Errorf("Expected channel number 99, got %d", c2.ChannelNumber)
	}

	// Verify Channel 3
	c3 := channels[2]
	if c3.Name != "Simple Channel No Attributes" {
		t.Errorf("Expected name 'Simple Channel No Attributes', got '%s'", c3.Name)
	}
	if c3.GroupID != "" {
		t.Errorf("Expected empty group, got '%s'", c3.GroupID)
	}
}

func TestParseM3U_MemoryFootprint(t *testing.T) {
	// Generate a 50,000 channel playlist string dynamically
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	for i := 1; i <= 50000; i++ {
		fmt.Fprintf(&builder, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-name=\"Channel %d\" tvg-logo=\"http://logo.com/ch%d.png\" group-title=\"Category %d\" tvg-chno=\"%d\",Channel %d\n", i, i, i, i%10, i, i)
		fmt.Fprintf(&builder, "http://stream.example.com/live/%d.m3u8\n", i)
	}
	playlistData := builder.String()

	// Measure heap memory before parsing
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Run the parser
	channels, err := ParseM3U(strings.NewReader(playlistData))
	if err != nil {
		t.Fatalf("ParseM3U failed: %v", err)
	}

	// Measure heap memory after parsing
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	var diffAllocKB int64
	if memAfter.Alloc > memBefore.Alloc {
		diffAllocKB = int64(memAfter.Alloc-memBefore.Alloc) / 1024
	} else {
		diffAllocKB = -int64(memBefore.Alloc-memAfter.Alloc) / 1024
	}
	t.Logf("Channels parsed: %d", len(channels))
	t.Logf("Heap Allocated memory diff: %d KB (%.2f MB)", diffAllocKB, float64(diffAllocKB)/1024)

	if len(channels) != 50000 {
		t.Errorf("Expected 50000 channels, got %d", len(channels))
	}

	// Check if memory usage diff is well within 50MB
	// Even with storing 50k channels, memory should be around 15-20MB
	if diffAllocKB > 50*1024 {
		t.Errorf("Memory footprint exceeds 50MB limit: %d KB", diffAllocKB)
	}
}

func BenchmarkParseM3U(b *testing.B) {
	// Setup benchmark data (1000 channels)
	var builder strings.Builder
	builder.WriteString("#EXTM3U\n")
	for i := 1; i <= 1000; i++ {
		fmt.Fprintf(&builder, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-name=\"Channel %d\" tvg-logo=\"http://logo.com/ch%d.png\" group-title=\"Category %d\" tvg-chno=\"%d\",Channel %d\n", i, i, i, i%10, i, i)
		fmt.Fprintf(&builder, "http://stream.example.com/live/%d.m3u8\n", i)
	}
	playlistData := builder.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader([]byte(playlistData))
		_, _ = ParseM3U(r)
	}
}
