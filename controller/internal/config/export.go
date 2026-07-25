package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"reflect"
	"sort"
	"strings"
)

type DocsReference struct {
	SchemaVersion int          `json:"schemaVersion"`
	SchemaSHA256  string       `json:"schemaSha256"`
	Sections      []DocSection `json:"sections"`
}

type DocSection struct {
	ID              string         `json:"id"`
	Title           string         `json:"title"`
	Anchor          string         `json:"anchor"`
	ConsoleDeepLink string         `json:"consoleDeepLink"`
	Overview        string         `json:"overview"`
	Details         []string       `json:"details"`
	ExemplarYAML    string         `json:"exemplarYaml"`
	Tradeoffs       []any          `json:"tradeoffs"`
	Examples        []any          `json:"examples"`
	Links           []any          `json:"links"`
	UI              map[string]any `json:"ui"`
	Settings        []DocSetting   `json:"settings"`
}

type DocSetting struct {
	Path            string         `json:"path"`
	Anchor          string         `json:"anchor"`
	ConsoleDeepLink string         `json:"consoleDeepLink"`
	Type            string         `json:"type"`
	Default         any            `json:"default"`
	Required        bool           `json:"required"`
	Choices         []any          `json:"choices"`
	Constraints     map[string]any `json:"constraints"`
	Restart         string         `json:"restart"`
	LegacyEnv       string         `json:"legacyEnv,omitempty"`
	Secret          bool           `json:"secret"`
	Overview        string         `json:"overview"`
	Details         []string       `json:"details"`
	Tradeoffs       []any          `json:"tradeoffs"`
	Examples        []any          `json:"examples"`
	Links           []any          `json:"links"`
	UI              map[string]any `json:"ui"`
}

func DocsJSON() ([]byte, error) {
	schema, err := EmbeddedSchema()
	if err != nil {
		return nil, err
	}
	projection, err := schema.docsProjection()
	if err != nil {
		return nil, err
	}
	output, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(output, '\n'), nil
}

func (s *Schema) docsProjection() (DocsReference, error) {
	result := DocsReference{SchemaVersion: 1, SchemaSHA256: s.Hash()}
	props := s.root["properties"].(map[string]any)
	for _, key := range orderedPropertyKeys(props) {
		if key == "version" {
			continue
		}
		node := props[key].(map[string]any)
		ui := node["x-vm-ui"].(map[string]any)
		doc := node["x-vm-doc"].(map[string]any)
		section := DocSection{
			ID: key, Title: stringValue(ui["section"]), Anchor: anchorFor(key),
			ConsoleDeepLink: "/config#" + anchorFor(key), Overview: stringValue(doc["overview"]),
			Details: stringArray(doc["details"]), Tradeoffs: anyArray(doc["tradeoffs"]),
			Examples: anyArray(doc["examples"]), Links: exportedLinks(doc["links"]),
			UI:       deepCopy(ui).(map[string]any),
			Settings: []DocSetting{},
		}
		for _, example := range section.Examples {
			if object, ok := example.(map[string]any); ok {
				if yaml, ok := object["yaml"].(string); ok {
					section.ExemplarYAML = yaml
					break
				}
			}
		}
		if section.ExemplarYAML == "" {
			section.ExemplarYAML = key + ": {}\n"
		}
		collectSettings(node, key, &section.Settings)
		result.Sections = append(result.Sections, section)
	}
	return result, nil
}

func collectSettings(node map[string]any, prefix string, target *[]DocSetting) {
	props, _ := node["properties"].(map[string]any)
	for _, key := range orderedPropertyKeys(props) {
		child := props[key].(map[string]any)
		settingPath := prefix + "." + key
		if child["type"] == "object" {
			collectSettings(child, settingPath, target)
			continue
		}
		doc := child["x-vm-doc"].(map[string]any)
		constraints := map[string]any{}
		for _, name := range []string{"minimum", "maximum", "minLength", "maxLength", "pattern", "uniqueItems"} {
			if value, ok := child[name]; ok {
				constraints[name] = value
			}
		}
		choices, _ := child["enum"].([]any)
		_, secret := child["x-vm-secret"]
		*target = append(*target, DocSetting{
			Path: settingPath, Anchor: anchorFor(settingPath), ConsoleDeepLink: "/config#" + anchorFor(settingPath),
			Type: stringValue(child["type"]), Default: child["default"], Required: true,
			Choices: choices, Constraints: constraints, Restart: stringValue(child["x-vm-restart"]),
			LegacyEnv: stringValue(child["x-vm-env"]), Secret: secret, Overview: stringValue(doc["overview"]),
			Details: stringArray(doc["details"]), Tradeoffs: anyArray(doc["tradeoffs"]),
			Examples: anyArray(doc["examples"]), Links: exportedLinks(doc["links"]),
			UI: deepCopy(child["x-vm-ui"]).(map[string]any),
		})
	}
}

func (s *Schema) UIJSON() ([]byte, error) {
	docs, err := s.docsProjection()
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"schemaVersion": docs.SchemaVersion,
		"schemaSha256":  docs.SchemaSHA256,
		"sections":      docs.Sections,
	}
	output, err := json.Marshal(result)
	return output, err
}

func RedactedEffective(loaded *Loaded) map[string]any {
	encoded, _ := json.Marshal(loaded.Config)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	for secretPath := range loaded.Secrets {
		setPath(result, secretPath, nil)
	}
	return result
}

func RestartServices(schema *Schema, startup, current RawConfig) []string {
	classes := map[string]bool{}
	collectRestartChanges(schema.root, startup, current, "", classes)
	if classes["all"] {
		for _, name := range []string{"desktop", "valkey", "llama", "ttsd", "mail", "controller"} {
			classes[name] = true
		}
	}
	order := []string{}
	if classes["desktop"] {
		order = append(order, "xvfb", "openbox", "x11vnc", "novnc", "chromium", "chromium-watchdog")
	}
	for _, class := range []string{"valkey", "llama", "ttsd", "mail"} {
		if classes[class] {
			order = append(order, class)
		}
	}
	if len(classes) > 0 {
		order = append(order, "controller")
	}
	return deduplicate(order)
}

func collectRestartChanges(schema map[string]any, old, current any, prefix string, classes map[string]bool) {
	if reflect.DeepEqual(normalizeTree(old), normalizeTree(current)) {
		return
	}
	if schema["type"] != "object" {
		if restart := stringValue(schema["x-vm-restart"]); restart != "" && restart != "none" {
			classes[restart] = true
		}
		return
	}
	oldObject := objectValue(old)
	currentObject := objectValue(current)
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		collectRestartChanges(raw.(map[string]any), oldObject[key], currentObject[key], joinPath(prefix, key), classes)
	}
}

func ChangedKeys(schema *Schema, old, current RawConfig) []string {
	var result []string
	collectChanged(schema.root, old, current, "", &result)
	sort.Strings(result)
	return result
}

func collectChanged(schema map[string]any, old, current any, prefix string, result *[]string) {
	if reflect.DeepEqual(normalizeTree(old), normalizeTree(current)) {
		return
	}
	if schema["type"] != "object" {
		*result = append(*result, prefix)
		return
	}
	oldObject := objectValue(old)
	currentObject := objectValue(current)
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		collectChanged(raw.(map[string]any), oldObject[key], currentObject[key], joinPath(prefix, key), result)
	}
}

func anchorFor(value string) string {
	var output strings.Builder
	for index, char := range value {
		if char == '.' || char == '_' || char == ' ' {
			output.WriteByte('-')
		} else if char >= 'A' && char <= 'Z' {
			if index > 0 {
				output.WriteByte('-')
			}
			output.WriteByte(byte(char + ('a' - 'A')))
		} else {
			output.WriteRune(char)
		}
	}
	return strings.Trim(output.String(), "-")
}

func exportedLinks(value any) []any {
	items := anyArray(value)
	result := make([]any, 0, len(items))
	for _, item := range items {
		link, ok := item.(map[string]any)
		if !ok {
			continue
		}
		copy := deepCopy(link).(map[string]any)
		href := stringValue(copy["href"])
		if !strings.HasPrefix(href, "https://") {
			clean := path.Clean(strings.TrimPrefix(href, "../../"))
			copy["href"] = "https://github.com/mayanklahiri/virtualme/blob/main/" + clean
		} else if _, err := url.Parse(href); err != nil {
			continue
		}
		result = append(result, copy)
	}
	return result
}

func stringArray(value any) []string {
	items := anyArray(value)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func anyArray(value any) []any {
	items, _ := value.([]any)
	if items == nil {
		return []any{}
	}
	return deepCopy(items).([]any)
}

func setPath(root map[string]any, dotted string, value any) {
	parts := strings.Split(dotted, ".")
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, _ := current[part].(map[string]any)
		if next == nil {
			return
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func normalizeTree(value any) any {
	encoded, _ := json.Marshal(value)
	var result any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	return result
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok {
		return object
	}
	if raw, ok := value.(RawConfig); ok {
		return map[string]any(raw)
	}
	return nil
}

func deduplicate(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	return result
}

func ValidateRaw(raw RawConfig, dataDir string, environment []string, resolver *Resolver) (*Loaded, []byte, error) {
	schema, err := EmbeddedSchema()
	if err != nil {
		return nil, nil, err
	}
	if err := schema.Validate(deferredValidationTree(schema.root, raw)); err != nil {
		return nil, nil, err
	}
	tempRoot := deepCopy(raw).(map[string]any)
	effective := deepCopy(raw).(map[string]any)
	env := environmentMap(environment)
	sources := map[string]Source{}
	markValueSources(tempRoot, "", SourceYAML, sources)
	if err := applyLegacy(schema.root, effective, "", env, sources, nil, tempRoot); err != nil {
		return nil, nil, err
	}
	secrets := map[string]SecretStatus{}
	owned := false
	if resolver == nil {
		resolver, owned = NewResolver(environment), true
	}
	if owned {
		defer resolver.Close()
	}
	if err := resolveTree(schema.root, effective, tempRoot, "", dataDir, env, resolver, secrets, effective); err != nil {
		return nil, nil, err
	}
	if err := schema.Validate(effective); err != nil {
		return nil, nil, err
	}
	if err := ValidateSemantic(effective); err != nil {
		return nil, nil, err
	}
	var typed Config
	encoded, _ := json.Marshal(effective)
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&typed); err != nil {
		return nil, nil, fmt.Errorf("typed config: %w", err)
	}
	canonical, err := schema.Emit(raw)
	if err != nil {
		return nil, nil, err
	}
	hash := sha256Hex(canonical)
	return &Loaded{Config: typed, Raw: deepCopy(raw).(map[string]any), Sources: sources, Secrets: secrets,
		Hash: hash, SchemaHash: schema.Hash(), DataDir: dataDir, File: path.Join(dataDir, FileName)}, canonical, nil
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}
