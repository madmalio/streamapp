package parser

import (
	"regexp"
)

// CategoryRule maps a regex pattern to a standard category name.
type CategoryRule struct {
	Regex    *regexp.Regexp
	Category string
}

var smartRules = []CategoryRule{
	// News (Check first so CNBC/MSNBC don't get caught by Local NBC)
	{regexp.MustCompile(`(?i)(CNN|Fox News|MSNBC|Weather|CBS News|ABC News|NBC News|Bloomberg|CNBC)`), "News"},
	// Sports
	{regexp.MustCompile(`(?i)(ESPN|FS1|FS2|NFL|NBA|MLB|NHL|Golf|Tennis|Sports|Bally)`), "Sports"},
	// Movies
	{regexp.MustCompile(`(?i)(HBO|Showtime|Starz|Cinemax|AMC|FX|TCM|Paramount|Hallmark|Movies)`), "Movies"},
	// Kids
	{regexp.MustCompile(`(?i)(Disney|Nickelodeon|Nick|Cartoon Network|PBS Kids|Boomerang)`), "Kids"},
	// Documentaries / Factual
	{regexp.MustCompile(`(?i)(Discovery|History|Nat Geo|Animal Planet|Science)`), "Documentary"},
	// Entertainment / Classics (no word boundaries, matches MeTV, Cozi, etc.)
	{regexp.MustCompile(`(?i)(MeTV|Cozi|Antenna|Grit|Bounce|Defy|Catchy|Laff|TLC|Bravo|E!|Syfy|USA|TBS|TNT)`), "Entertainment"},
	// Local / Network (no word boundaries, matches KABC, WCBS, NBC2)
	{regexp.MustCompile(`(?i)(NBC|ABC|CBS|FOX|PBS|CW|Telemundo|Univision|Ion)`), "Local"},
}

// SmartCategorize inspects a channel name and returns a standard category if a match is found.
// If no match is found, it returns the provided default category, or "Other" if the default is empty.
func SmartCategorize(channelName string, defaultCategory string) string {
	for _, rule := range smartRules {
		if rule.Regex.MatchString(channelName) {
			return rule.Category
		}
	}
	
	if defaultCategory != "" {
		return defaultCategory
	}
	return "Other"
}
