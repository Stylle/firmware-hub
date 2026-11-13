// Package updatepath validates portable package paths, independently of the host OS.
package updatepath

import (
	"strings"
	"unicode/utf8"
)

func Valid(s string) bool {
	if len(s) == 0 || len(s) > 240 || !utf8.ValidString(s) || strings.ContainsAny(s, "\\:\x00<>\"|?*") || strings.HasPrefix(s, "/") {
		return false
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.HasPrefix(strings.ToLower(part), ".firmware-hub-") {
			return false
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CLOCK$" || stem == "CONIN$" || stem == "CONOUT$" || ((strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && (strings.ContainsAny(strings.TrimPrefix(strings.TrimPrefix(stem, "COM"), "LPT"), "¹²³") || len(stem) == 4 && stem[3] >= '0' && stem[3] <= '9')) {
			return false
		}
	}
	return true
}
