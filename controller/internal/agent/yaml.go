package agent

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const readPageCap = 16000

var yamlKeyOrder = map[string][]string{
	"":     {"title", "url", "head", "body"},
	"head": {"lang", "description", "canonical", "og"},
	"node": {
		"tag", "sel", "text", "href", "src", "alt", "type", "name", "value",
		"placeholder", "action", "method", "label", "rows", "items", "children", "note",
	},
}

//go:embed js/readpage.js
var readPageScript string

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
		quoted, err := json.Marshal(typed)
		if err != nil {
			quoted = []byte(`"` + strings.ReplaceAll(typed, `"`, `\"`) + `"`)
		}
		return string(quoted)
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
		quoted, _ := json.Marshal(fmt.Sprint(typed))
		return string(quoted)
	}
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
	pad := strings.Repeat("  ", indent)
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
		if strings.HasPrefix(encoded, "- ") || strings.HasPrefix(encoded, "-\n") {
			lines = append(lines, fmt.Sprintf("%s%s:\n%s", pad, key, encoded))
			continue
		}
		if strings.Contains(encoded, "\n") {
			lines = append(lines, fmt.Sprintf("%s%s:\n%s", pad, key, encoded))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s%s: %s", pad, key, encoded))
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
	pad := strings.Repeat("  ", indent)
	lines := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			inner := encodeMap(typed, indent+1, "node")
			innerLines := strings.Split(inner, "\n")
			if len(innerLines) == 0 {
				continue
			}
			lines = append(lines, pad+"- "+strings.TrimPrefix(innerLines[0], strings.Repeat("  ", indent+1)))
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
	text := encodeYAML(digest)
	for len(text) > readPageCap {
		if !dropLastBodyNode(digest) {
			break
		}
		text = encodeYAML(digest)
	}
	if len(text) > readPageCap {
		return text[:readPageCap]
	}
	if needsBudgetMarker(digest) && len(text)+len(`note: "truncated: page digest exceeded budget"`) <= readPageCap {
		appendBudgetMarker(digest)
		text = encodeYAML(digest)
	}
	if len(text) > readPageCap {
		for len(text) > readPageCap && dropLastBodyNode(digest) {
			text = encodeYAML(digest)
		}
		appendBudgetMarker(digest)
		text = encodeYAML(digest)
		if len(text) > readPageCap {
			return text[:readPageCap]
		}
	}
	return text
}

func needsBudgetMarker(digest map[string]any) bool {
	body, ok := digest["body"].([]any)
	if !ok || len(body) == 0 {
		return false
	}
	last, ok := body[len(body)-1].(map[string]any)
	if !ok {
		return true
	}
	note, _ := last["note"].(string)
	return note != "truncated: page digest exceeded budget"
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
