package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxConfigBytes = 1 << 20

type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type yamlLine struct {
	number int
	indent int
	text   string
}

type yamlParser struct {
	lines []yamlLine
	at    int
	locs  map[string]Location
}

var (
	keyPattern     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	numberPattern  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)\.[0-9]+$`)
)

func ParseYAML(input []byte) (RawConfig, map[string]Location, error) {
	if len(input) > maxConfigBytes {
		return nil, nil, fmt.Errorf("config YAML exceeds 1 MiB")
	}
	if !utf8.Valid(input) {
		return nil, nil, fmt.Errorf("config YAML is not valid UTF-8")
	}
	input = bytes.TrimPrefix(input, []byte{0xef, 0xbb, 0xbf})
	text := strings.ReplaceAll(string(input), "\r\n", "\n")
	if strings.ContainsRune(text, '\r') {
		return nil, nil, fmt.Errorf("config YAML contains bare carriage return")
	}
	if strings.ContainsRune(text, '\t') {
		return nil, nil, fmt.Errorf("config YAML contains a tab")
	}
	var lines []yamlLine
	for index, raw := range strings.Split(text, "\n") {
		content, err := stripYAMLComment(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("line %d: %w", index+1, err)
		}
		content = strings.TrimRight(content, " ")
		if strings.TrimSpace(content) == "" {
			continue
		}
		indent := len(content) - len(strings.TrimLeft(content, " "))
		if indent%2 != 0 {
			return nil, nil, fmt.Errorf("line %d column %d: indentation must use two spaces", index+1, indent+1)
		}
		trimmed := content[indent:]
		if forbiddenYAMLSyntax(trimmed) {
			return nil, nil, fmt.Errorf("line %d column %d: unsupported YAML syntax", index+1, indent+1)
		}
		lines = append(lines, yamlLine{number: index + 1, indent: indent, text: trimmed})
	}
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("config YAML root must be a mapping")
	}
	parser := &yamlParser{lines: lines, locs: map[string]Location{}}
	value, err := parser.parseBlock(0, "")
	if err != nil {
		return nil, nil, err
	}
	if parser.at != len(lines) {
		line := lines[parser.at]
		return nil, nil, fmt.Errorf("line %d column %d: trailing content", line.number, line.indent+1)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("config YAML root must be a mapping")
	}
	return root, parser.locs, nil
}

func stripYAMLComment(line string) (string, error) {
	single, double, escaped := false, false, false
	for index, char := range line {
		if double {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == '"' {
				double = false
			}
			continue
		}
		if single {
			if char == '\'' {
				single = false
			}
			continue
		}
		switch char {
		case '"':
			double = true
		case '\'':
			single = true
		case '#':
			if index == 0 || line[index-1] == ' ' {
				return line[:index], nil
			}
		}
	}
	if single || double || escaped {
		return "", fmt.Errorf("unclosed quoted scalar")
	}
	return line, nil
}

func forbiddenYAMLSyntax(text string) bool {
	if text == "---" || text == "..." || strings.HasPrefix(text, "%") ||
		strings.HasPrefix(text, "? ") || strings.HasPrefix(text, "<<:") ||
		strings.Contains(text, " &") || strings.Contains(text, " *") ||
		strings.Contains(text, ": !") ||
		strings.HasPrefix(text, "&") || strings.HasPrefix(text, "*") ||
		strings.HasPrefix(text, "!") || strings.Contains(text, ": |") ||
		strings.Contains(text, ": >") {
		return true
	}
	return false
}

func (p *yamlParser) parseBlock(indent int, path string) (any, error) {
	if p.at >= len(p.lines) {
		return nil, fmt.Errorf("unexpected end of YAML")
	}
	if p.lines[p.at].indent != indent {
		line := p.lines[p.at]
		return nil, fmt.Errorf("line %d column %d at %s: indentation jump", line.number, line.indent+1, path)
	}
	if strings.HasPrefix(p.lines[p.at].text, "-") {
		return p.parseSequence(indent, path)
	}
	return p.parseMapping(indent, path)
}

func (p *yamlParser) parseMapping(indent int, path string) (map[string]any, error) {
	result := map[string]any{}
	for p.at < len(p.lines) {
		line := p.lines[p.at]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, fmt.Errorf("line %d column %d at %s: indentation jump", line.number, line.indent+1, path)
		}
		if strings.HasPrefix(line.text, "-") {
			return nil, fmt.Errorf("line %d column %d at %s: mixed mapping and sequence", line.number, line.indent+1, path)
		}
		colon := strings.IndexByte(line.text, ':')
		if colon < 1 {
			return nil, fmt.Errorf("line %d column %d at %s: expected mapping key", line.number, line.indent+1, path)
		}
		key := line.text[:colon]
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("line %d column %d at %s: invalid mapping key %q", line.number, line.indent+1, path, key)
		}
		childPath := joinPath(path, key)
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("line %d column %d at %s: duplicate key", line.number, line.indent+1, childPath)
		}
		p.locs[childPath] = Location{Line: line.number, Column: line.indent + 1}
		rest := strings.TrimSpace(line.text[colon+1:])
		p.at++
		if rest == "" {
			if p.at >= len(p.lines) || p.lines[p.at].indent <= indent {
				return nil, fmt.Errorf("line %d column %d at %s: missing nested value", line.number, line.indent+colon+2, childPath)
			}
			if p.lines[p.at].indent != indent+2 {
				next := p.lines[p.at]
				return nil, fmt.Errorf("line %d column %d at %s: indentation jump", next.number, next.indent+1, childPath)
			}
			value, err := p.parseBlock(indent+2, childPath)
			if err != nil {
				return nil, err
			}
			result[key] = value
		} else {
			value, err := parseYAMLScalar(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d column %d at %s: %w", line.number, line.indent+colon+2, childPath, err)
			}
			result[key] = value
		}
	}
	return result, nil
}

func (p *yamlParser) parseSequence(indent int, path string) ([]any, error) {
	var result []any
	for p.at < len(p.lines) {
		line := p.lines[p.at]
		if line.indent < indent {
			break
		}
		if line.indent != indent || !strings.HasPrefix(line.text, "-") {
			return nil, fmt.Errorf("line %d column %d at %s: malformed sequence", line.number, line.indent+1, path)
		}
		if len(line.text) > 1 && line.text[1] != ' ' {
			return nil, fmt.Errorf("line %d column %d at %s: malformed sequence marker", line.number, line.indent+1, path)
		}
		itemPath := fmt.Sprintf("%s[%d]", path, len(result))
		p.locs[itemPath] = Location{Line: line.number, Column: line.indent + 1}
		rest := strings.TrimSpace(line.text[1:])
		p.at++
		if rest == "" {
			if p.at >= len(p.lines) || p.lines[p.at].indent != indent+2 {
				return nil, fmt.Errorf("line %d column %d at %s: missing sequence value", line.number, line.indent+1, itemPath)
			}
			value, err := p.parseBlock(indent+2, itemPath)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		} else {
			value, err := parseYAMLScalar(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d column %d at %s: %w", line.number, line.indent+3, itemPath, err)
			}
			result = append(result, value)
		}
	}
	return result, nil
}

func parseYAMLScalar(text string) (any, error) {
	switch text {
	case "{}":
		return map[string]any{}, nil
	case "[]":
		return []any{}, nil
	case "null":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") {
		return nil, fmt.Errorf("general flow collections are unsupported")
	}
	if strings.HasPrefix(text, `"`) {
		var result string
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			return nil, fmt.Errorf("invalid double-quoted string: %w", err)
		}
		return result, nil
	}
	if strings.HasPrefix(text, "'") {
		if len(text) < 2 || !strings.HasSuffix(text, "'") {
			return nil, fmt.Errorf("unclosed single-quoted string")
		}
		return strings.ReplaceAll(text[1:len(text)-1], "''", "'"), nil
	}
	if integerPattern.MatchString(text) {
		number, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("integer outside signed 64-bit range")
		}
		return number, nil
	}
	if numberPattern.MatchString(text) {
		number, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid finite decimal")
		}
		return number, nil
	}
	if strings.Contains(text, ": ") || strings.HasPrefix(text, "- ") ||
		strings.TrimSpace(text) != text {
		return nil, fmt.Errorf("ambiguous plain scalar")
	}
	return text, nil
}

func (s *Schema) Emit(raw RawConfig) ([]byte, error) {
	if err := s.Validate(deferredValidationTree(s.root, raw)); err != nil {
		return nil, err
	}
	var output strings.Builder
	emitObject(&output, s.root, raw, 0, true)
	return []byte(strings.TrimRight(output.String(), "\n") + "\n"), nil
}

func emitObject(output *strings.Builder, schema map[string]any, value map[string]any, indent int, comments bool) {
	props, _ := schema["properties"].(map[string]any)
	for _, key := range orderedPropertyKeys(props) {
		child := props[key].(map[string]any)
		item := value[key]
		if comments {
			emitComments(output, child, indent)
		}
		prefix := strings.Repeat(" ", indent) + key + ":"
		switch typed := item.(type) {
		case map[string]any:
			if len(typed) == 0 {
				fmt.Fprintf(output, "%s {}\n", prefix)
			} else {
				fmt.Fprintln(output, prefix)
				emitObject(output, child, typed, indent+2, comments)
			}
		case []any:
			if len(typed) == 0 {
				fmt.Fprintf(output, "%s []\n", prefix)
			} else {
				fmt.Fprintln(output, prefix)
				for _, element := range typed {
					fmt.Fprintf(output, "%s- %s\n", strings.Repeat(" ", indent+2), emitScalar(element, child))
				}
			}
		default:
			fmt.Fprintf(output, "%s %s\n", prefix, emitScalar(item, child))
		}
	}
}

func emitComments(output *strings.Builder, schema map[string]any, indent int) {
	doc, _ := schema["x-vm-doc"].(map[string]any)
	lines := []string{}
	if schema["type"] == "object" {
		lines = append(lines, stringValue(doc["overview"]))
		lines = append(lines, stringArray(doc["details"])...)
	} else {
		lines = append(lines, stringValue(schema["description"]))
		lines = append(lines, stringValue(doc["overview"]))
		lines = append(lines, stringArray(doc["details"])...)
		lines = append(lines, "Default: "+emitScalar(schema["default"], schema)+
			". Restart impact: "+stringValue(schema["x-vm-restart"])+".")
		if choices, ok := schema["enum"].([]any); ok && len(choices) > 0 {
			parts := make([]string, len(choices))
			for index, choice := range choices {
				parts[index] = emitScalar(choice, schema)
			}
			lines = append(lines, "Choices: "+strings.Join(parts, ", ")+".")
		}
		for _, raw := range anyArray(doc["tradeoffs"]) {
			tradeoff, _ := raw.(map[string]any)
			lines = append(lines, fmt.Sprintf("Tradeoff %s: %s Cost: %s",
				stringValue(tradeoff["choice"]), stringValue(tradeoff["benefit"]), stringValue(tradeoff["cost"])))
		}
	}
	for _, line := range lines {
		for _, wrapped := range wrapComment(strings.TrimSpace(line), 88-indent-2) {
			fmt.Fprintf(output, "%s# %s\n", strings.Repeat(" ", indent), wrapped)
		}
	}
}

func wrapComment(text string, width int) []string {
	if text == "" {
		return nil
	}
	if width < 20 {
		width = 20
	}
	words := strings.Fields(text)
	lines := []string{}
	current := ""
	for _, word := range words {
		if current != "" && len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
		} else if current == "" {
			current = word
		} else {
			current += " " + word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func orderedPropertyKeys(props map[string]any) []string {
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sortByOrder(keys, func(key string) int64 {
		node := props[key].(map[string]any)
		ui, _ := node["x-vm-ui"].(map[string]any)
		order, _ := integerValue(ui["order"])
		return order
	})
	return keys
}

func sortByOrder(keys []string, order func(string) int64) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && (order(keys[j]) < order(keys[j-1]) ||
			(order(keys[j]) == order(keys[j-1]) && keys[j] < keys[j-1])); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

func emitScalar(value any, schema map[string]any) string {
	if value == nil {
		return "null"
	}
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case string:
		if _, secret := schema["x-vm-secret"]; secret || !plainSafe(typed) {
			encoded, _ := json.Marshal(typed)
			return string(encoded)
		}
		return typed
	default:
		encoded, _ := json.Marshal(typed)
		return string(encoded)
	}
}

func plainSafe(text string) bool {
	if text == "" || strings.TrimSpace(text) != text || strings.Contains(text, "#") ||
		strings.Contains(text, ": ") || text == "{}" || text == "[]" ||
		text == "null" || text == "true" || text == "false" ||
		integerPattern.MatchString(text) || numberPattern.MatchString(text) {
		return false
	}
	return !strings.ContainsAny(text, "\r\n\t")
}
