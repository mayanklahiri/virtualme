package telegram

import (
	"strings"
	"unicode"
	"unicode/utf16"
)

func ChunkText(text string) []string {
	if text == "" {
		return nil
	}
	var chunks []string
	for text != "" {
		runes := []rune(text)
		units := 0
		end := 0
		for index, char := range runes {
			next := 1
			if char > 0xFFFF {
				next = 2
			}
			if units+next > 4096 {
				break
			}
			units += next
			end = index + 1
		}
		if end == len(runes) {
			chunks = append(chunks, text)
			break
		}
		candidate := runes[:end]
		floorUnits := 4096 * 3 / 4
		boundary := preferredBoundary(candidate, floorUnits)
		if boundary > 0 {
			end = boundary
		}
		chunk := string(runes[:end])
		chunks = append(chunks, chunk)
		text = text[len(chunk):]
	}
	return chunks
}

func preferredBoundary(runes []rune, floorUnits int) int {
	unitsAt := make([]int, len(runes)+1)
	for i, char := range runes {
		unitsAt[i+1] = unitsAt[i] + len(utf16.Encode([]rune{char}))
	}
	latest := func(match func(int) bool) int {
		for i := len(runes); i > 0; i-- {
			if unitsAt[i] >= floorUnits && match(i) {
				return i
			}
		}
		return 0
	}
	if index := latest(func(i int) bool { return i >= 2 && string(runes[i-2:i]) == "\n\n" }); index > 0 {
		return index
	}
	if index := latest(func(i int) bool { return runes[i-1] == '\n' }); index > 0 {
		return index
	}
	return latest(func(i int) bool { return unicode.IsSpace(runes[i-1]) && !strings.ContainsRune("\n", runes[i-1]) })
}
