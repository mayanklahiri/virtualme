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

func TestChunkTextBoundaryPriority(t *testing.T) {
	prefix := strings.Repeat("a", 3100)
	input := prefix + " " + strings.Repeat("b", 100) + "\n" + strings.Repeat("c", 100) + "\n\n" + strings.Repeat("d", 1000)
	chunks := ChunkText(input)
	wantEnd := prefix + " " + strings.Repeat("b", 100) + "\n" + strings.Repeat("c", 100) + "\n\n"
	if len(chunks) < 2 || chunks[0] != wantEnd {
		t.Fatalf("double-newline boundary was not preferred: first length=%d", len([]rune(chunks[0])))
	}
	longToken := strings.Repeat("😀", 2049)
	chunks = ChunkText(longToken)
	if len(chunks) != 2 || len(utf16.Encode([]rune(chunks[0]))) != 4096 {
		t.Fatalf("long token chunks = %v", []int{len(utf16.Encode([]rune(chunks[0]))), len(utf16.Encode([]rune(chunks[1])))})
	}
}
