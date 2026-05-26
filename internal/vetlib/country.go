package vetlib

import "unicode/utf8"

// CountryFromName extracts an ISO-3166-1 alpha-2 country code from a
// flag emoji embedded in the URL fragment's friendly name (e.g.
// "🇩🇪 Berlin via foo" → "DE"). Returns "" if no flag is found.
//
// Flag emoji are encoded as two regional-indicator-symbol code points
// (U+1F1E6 'A' .. U+1F1FF 'Z'), so "🇩🇪" is U+1F1E9 U+1F1EA — D, E.
// We scan for the first occurrence of two consecutive indicator chars;
// public lists sometimes prepend Persian text or ad spam before the
// flag, so anchoring at offset 0 would miss real entries.
//
// Names with two separate flags ("🇩🇪🔑Tel@…") only return the first.
// That matches user intent — the leading flag is the country claim,
// later emoji are decoration.
func CountryFromName(name string) string {
	const (
		regStart = 0x1F1E6
		regEnd   = 0x1F1FF
	)
	var prev rune
	for _, r := range name {
		if r >= regStart && r <= regEnd {
			if prev >= regStart && prev <= regEnd {
				return string([]byte{
					byte('A' + prev - regStart),
					byte('A' + r - regStart),
				})
			}
		}
		prev = r
	}
	return ""
}

// FlagEmoji returns the two-rune flag emoji for an ISO-3166-1 alpha-2
// country code, or "" if cc is malformed. The inverse of
// CountryFromName for rendering grouped headers.
func FlagEmoji(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	c0, c1 := cc[0], cc[1]
	if c0 < 'A' || c0 > 'Z' || c1 < 'A' || c1 > 'Z' {
		return ""
	}
	var b [8]byte
	n := utf8.EncodeRune(b[:], 0x1F1E6+rune(c0-'A'))
	n += utf8.EncodeRune(b[n:], 0x1F1E6+rune(c1-'A'))
	return string(b[:n])
}
