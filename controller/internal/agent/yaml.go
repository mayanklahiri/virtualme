package agent

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const readPageCap = 64000

var yamlKeyOrder = map[string][]string{
	"":     {"title", "url", "head", "body"},
	"head": {"lang", "description", "canonical", "og"},
	"node": {
		"tag", "rank", "title", "title_link", "url", "score", "comments", "comment_url",
		"author", "age", "text", "href", "src", "alt", "type", "name", "value",
		"placeholder", "action", "method", "label", "rows", "items", "children", "note",
	},
}

//go:embed js/readpage.js
var readPageScript string

//go:embed js/domdump.js
var domDumpScript string

func encodeYAML(value any) string {
	return strings.TrimSpace(encodeValue(value, 0, "")) + "\n"
}

func encodeValue(value any, indent int, context string) string {
	switch typed := value.(type) {
	case map[string]any:
		return encodeMap(typed, indent, context)
	case []any:
		return encodeSequence(typed, indent, context)
	case string:
		return quoteString(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return quoteString(fmt.Sprint(typed))
	}
}

// quoteString emits a JSON string without HTML escaping: selector paths are
// full of ">" and escaping each one as \u003e wastes five bytes of the digest
// budget per separator.
func quoteString(value string) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return strings.TrimRight(buffer.String(), "\n")
}

func orderedKeys(mapping map[string]any, context string) []string {
	priority := yamlKeyOrder[context]
	seen := make(map[string]struct{}, len(mapping))
	keys := make([]string, 0, len(mapping))
	for _, key := range priority {
		if _, ok := mapping[key]; ok {
			keys = append(keys, key)
			seen[key] = struct{}{}
		}
	}
	rest := make([]string, 0, len(mapping))
	for key := range mapping {
		if _, ok := seen[key]; ok {
			continue
		}
		rest = append(rest, key)
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func encodeMap(mapping map[string]any, indent int, context string) string {
	if len(mapping) == 0 {
		return "{}"
	}
	pad := strings.Repeat("\t", indent)
	lines := make([]string, 0, len(mapping))
	for _, key := range orderedKeys(mapping, context) {
		value := mapping[key]
		nextContext := context
		if context == "" && (key == "head" || key == "body") {
			nextContext = key
		} else if context == "head" && key == "og" {
			nextContext = "head"
		} else if context == "body" || context == "head" {
			nextContext = "node"
		}
		encoded := encodeNested(value, indent+1, nextContext)
		// Nested mappings and sequences always start on their own line; a
		// single-key nested map has no newline yet must still be a block.
		switch value.(type) {
		case map[string]any, []any:
			if encoded == "{}" || encoded == "[]" {
				lines = append(lines, fmt.Sprintf("%s%s: %s", pad, key, encoded))
				continue
			}
			lines = append(lines, fmt.Sprintf("%s%s:\n%s", pad, key, encoded))
		default:
			lines = append(lines, fmt.Sprintf("%s%s: %s", pad, key, encoded))
		}
	}
	return strings.Join(lines, "\n")
}

func encodeNested(value any, indent int, context string) string {
	switch typed := value.(type) {
	case map[string]any:
		return encodeMap(typed, indent, context)
	case []any:
		return encodeSequence(typed, indent, context)
	default:
		return encodeValue(value, indent, context)
	}
}

func encodeSequence(items []any, indent int, context string) string {
	if len(items) == 0 {
		return "[]"
	}
	pad := strings.Repeat("\t", indent)
	lines := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			inner := encodeMap(typed, indent+1, "node")
			innerLines := strings.Split(inner, "\n")
			if len(innerLines) == 0 {
				continue
			}
			lines = append(lines, pad+"- "+strings.TrimPrefix(innerLines[0], strings.Repeat("\t", indent+1)))
			for _, line := range innerLines[1:] {
				if line == "" {
					continue
				}
				lines = append(lines, line)
			}
		case []any:
			nested := encodeSequence(typed, indent+1, context)
			lines = append(lines, pad+"-")
			for _, line := range strings.Split(nested, "\n") {
				if line == "" {
					continue
				}
				lines = append(lines, line)
			}
		default:
			lines = append(lines, pad+"- "+encodeValue(typed, 0, context))
		}
	}
	return strings.Join(lines, "\n")
}

func digestToYAML(digest map[string]any) string {
	pruneEmpty(digest)
	text := encodeYAML(digest)
	if len(text) <= readPageCap {
		return text
	}
	// Reserve room for the marker line so the marker itself never overflows.
	markerRoom := len("\t- note: \"truncated: page digest exceeded budget\"") + 1
	for len(text)+markerRoom > readPageCap && dropLastBodyNode(digest) {
		text = encodeYAML(digest)
	}
	appendBudgetMarker(digest)
	text = encodeYAML(digest)
	if len(text) > readPageCap {
		return text[:readPageCap]
	}
	return text
}

// pruneEmpty drops empty strings, sequences, and mappings recursively so the
// emitter never produces flow-style "{}"/"[]" outside the §4 subset.
func pruneEmpty(mapping map[string]any) bool {
	for key, item := range mapping {
		switch typed := item.(type) {
		case map[string]any:
			if pruneEmpty(typed) {
				delete(mapping, key)
			}
		case []any:
			pruned := pruneSeq(typed)
			if len(pruned) == 0 {
				delete(mapping, key)
			} else {
				mapping[key] = pruned
			}
		case string:
			if typed == "" {
				delete(mapping, key)
			}
		case nil:
			delete(mapping, key)
		}
	}
	return len(mapping) == 0
}

func pruneSeq(items []any) []any {
	kept := items[:0]
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			if !pruneEmpty(typed) {
				kept = append(kept, item)
			}
		case []any:
			pruned := pruneSeq(typed)
			if len(pruned) > 0 {
				kept = append(kept, any(pruned))
			}
		case nil:
		default:
			// Strings stay even when empty: row cells are positional and a
			// quoted empty scalar is inside the §4 subset.
			kept = append(kept, item)
		}
	}
	return kept
}

func appendBudgetMarker(digest map[string]any) {
	body, ok := digest["body"].([]any)
	if !ok {
		body = []any{}
	}
	body = append(body, map[string]any{"note": "truncated: page digest exceeded budget"})
	digest["body"] = body
}

func dropLastBodyNode(digest map[string]any) bool {
	body, ok := digest["body"].([]any)
	if !ok || len(body) == 0 {
		return false
	}
	if dropDeepestLast(body) {
		digest["body"] = body
		return true
	}
	digest["body"] = body[:len(body)-1]
	return len(body) > 0
}

func dropDeepestLast(nodes []any) bool {
	for index := len(nodes) - 1; index >= 0; index-- {
		node, ok := nodes[index].(map[string]any)
		if !ok {
			nodes = append(nodes[:index], nodes[index+1:]...)
			return true
		}
		for _, key := range []string{"children", "items", "rows"} {
			child, ok := node[key]
			if !ok {
				continue
			}
			switch typed := child.(type) {
			case []any:
				if len(typed) == 0 {
					continue
				}
				if dropDeepestLast(typed) {
					if len(typed) == 0 {
						delete(node, key)
					} else {
						node[key] = typed
					}
					return true
				}
			}
		}
	}
	return false
}

func readPageExpression() string {
	script := strings.TrimSpace(readPageScript)
	if strings.HasPrefix(script, "(() =>") {
		return script
	}
	return "(() => {" + script + "})()"
}
