package telegram

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestChunkTextLosslessUTF16Bound(t *testing.T) {
	cases := []string{
		"", strings.Repeat("a", 4096), strings.Repeat("a", 4097),
		strings.Repeat("😀", 3000), strings.Repeat("界", 5000),
		strings.Repeat("a", 3500) + "\n\n" + strings.Repeat("b", 1000),
		"a\u0301" + strings.Repeat("z", 5000),
	}
	for _, input := range cases {
		chunks := ChunkText(input)
		if strings.Join(chunks, "") != input {
			t.Fatal("chunking was not lossless")
		}
		for _, chunk := range chunks {
			if chunk == "" || len(utf16.Encode([]rune(chunk))) > 4096 {
				t.Fatalf("invalid chunk size %d", len(utf16.Encode([]rune(chunk))))
			}
		}
		if input == "" && len(chunks) != 0 {
			t.Fatal("empty input returned chunks")
		}
	}
}
