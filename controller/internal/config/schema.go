package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Schema struct {
	root map[string]any
	hash string
}

var (
	schemaOnce sync.Once
	schemaInst *Schema
	schemaErr  error
)

func EmbeddedSchema() (*Schema, error) {
	schemaOnce.Do(func() {
		decoder := json.NewDecoder(bytes.NewReader(embeddedSchema))
		decoder.UseNumber()
		var root map[string]any
		if err := decoder.Decode(&root); err != nil {
			schemaErr = fmt.Errorf("config schema: %w", err)
			return
		}
		sum := sha256.Sum256(embeddedSchema)
		candidate := &Schema{root: root, hash: hex.EncodeToString(sum[:])}
		if err := candidate.metaValidate(); err != nil {
			schemaErr = fmt.Errorf("config schema: %w", err)
			return
		}
		schemaInst = candidate
	})
	return schemaInst, schemaErr
}

func (s *Schema) Hash() string { return s.hash }

func (s *Schema) Root() map[string]any {
	return deepCopy(s.root).(map[string]any)
}

var supportedKeywords = map[string]bool{
	"$schema": true, "$id": true, "$defs": true, "$ref": true,
	"title": true, "description": true, "type": true, "properties": true,
	"required": true, "additionalProperties": true, "items": true,
	"uniqueItems": true, "enum": true, "const": true, "minimum": true,
	"maximum": true, "minLength": true, "maxLength": true, "pattern": true,
	"default": true, "x-vm-doc": true, "x-vm-ui": true, "x-vm-restart": true,
	"x-vm-env": true, "x-vm-secret": true, "x-vm-sensitive": true,
	"x-vm-integration": true,
}

var allowedTypes = map[string]bool{
	"object": true, "array": true, "string": true, "integer": true,
	"number": true, "boolean": true, "null": true,
}

func (s *Schema) metaValidate() error {
	if s.root["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		return errors.New("$schema must be draft 2020-12")
	}
	if s.root["$id"] != "https://virtualme.local/schemas/config-v1.json" {
		return errors.New("$id must identify config v1")
	}
	if strings.TrimSpace(stringValue(s.root["title"])) == "" ||
		strings.TrimSpace(stringValue(s.root["description"])) == "" {
		return errors.New("root title and description must be non-empty strings")
	}
	envPaths := map[string]string{}
	if err := s.metaNode(s.root, "", envPaths, map[string]bool{}); err != nil {
		return err
	}
	if defs, ok := s.root["$defs"].(map[string]any); ok {
		for name, raw := range defs {
			node, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("$defs.%s: definition must be an object", name)
			}
			if err := s.metaNode(node, "$defs."+name, envPaths, map[string]bool{"#/$defs/" + name: true}); err != nil {
				return err
			}
		}
	}
	if err := s.validateSecretConditions(); err != nil {
		return err
	}
	return s.validateExamples()
}

func (s *Schema) metaNode(node map[string]any, path string, envPaths map[string]string, refs map[string]bool) error {
	for key := range node {
		if !supportedKeywords[key] {
			return fmt.Errorf("%s: unsupported keyword %q", path, key)
		}
	}
	if ref, ok := node["$ref"].(string); ok {
		if len(node) != 1 {
			return fmt.Errorf("%s: $ref may not have sibling keywords", path)
		}
		if !strings.HasPrefix(ref, "#/$defs/") {
			return fmt.Errorf("%s: reference must target $defs", path)
		}
		if refs[ref] {
			return fmt.Errorf("%s: cyclic reference %s", path, ref)
		}
		target, err := s.resolveRef(ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		next := cloneBoolMap(refs)
		next[ref] = true
		return s.metaNode(target, path, envPaths, next)
	}
	kind, ok := node["type"].(string)
	if !ok || !allowedTypes[kind] {
		return fmt.Errorf("%s: invalid type", path)
	}
	if err := validateKeywordShapes(node, kind, path); err != nil {
		return err
	}
	if pattern, ok := node["pattern"].(string); ok {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s: bad pattern: %w", path, err)
		}
	}
	if unique, ok := node["uniqueItems"]; ok && unique != true {
		return fmt.Errorf("%s: uniqueItems must be true", path)
	}
	if restart, ok := node["x-vm-restart"].(string); ok {
		if !contains([]string{"none", "controller", "ttsd", "llama", "desktop", "valkey", "mail", "all"}, restart) {
			return fmt.Errorf("%s: invalid x-vm-restart", path)
		}
		ui, _ := node["x-vm-ui"].(map[string]any)
		if restart == "none" && stringValue(ui["component"]) != "vm-readonly-field" {
			return fmt.Errorf("%s: x-vm-restart none requires display-only metadata", path)
		}
	} else if kind != "object" {
		return fmt.Errorf("%s: leaf missing x-vm-restart", path)
	}
	if path != "" {
		if err := validateDoc(node["x-vm-doc"], node["enum"], path); err != nil {
			return err
		}
		if err := validateUI(node["x-vm-ui"], kind, path); err != nil {
			return err
		}
	}
	if env, ok := node["x-vm-env"].(string); ok {
		if matched, _ := regexp.MatchString(`^[A-Z][A-Z0-9_]*$`, env); !matched {
			return fmt.Errorf("%s: invalid x-vm-env", path)
		}
		if previous := envPaths[env]; previous != "" && !(env == "VM_XDOTOOL" &&
			((previous == "agent.xdotoolPath" && path == "health.xdotoolPath") ||
				(previous == "health.xdotoolPath" && path == "agent.xdotoolPath"))) {
			return fmt.Errorf("%s: duplicate x-vm-env %s", path, env)
		}
		envPaths[env] = path
	}
	if secret, ok := node["x-vm-secret"]; ok {
		if kind != "string" {
			return fmt.Errorf("%s: x-vm-secret requires string", path)
		}
		m, ok := secret.(map[string]any)
		if !ok || !(exactKeys(m, "cacheTtlSeconds", "allowEnv", "allowFile") ||
			exactKeys(m, "cacheTtlSeconds", "allowEnv", "allowFile", "resolveWhen")) {
			return fmt.Errorf("%s: malformed x-vm-secret", path)
		}
		ttl, ok := integerValue(m["cacheTtlSeconds"])
		if !ok || ttl < 0 || ttl > 86400 || m["allowEnv"] != true || m["allowFile"] != true {
			return fmt.Errorf("%s: malformed x-vm-secret", path)
		}
		ui, _ := node["x-vm-ui"].(map[string]any)
		if stringValue(ui["component"]) != "vm-secret-reference" || node["x-vm-sensitive"] != "credential" {
			return fmt.Errorf("%s: secret requires credential sensitivity and secret-reference UI", path)
		}
		if condition, exists := m["resolveWhen"]; exists {
			check, ok := condition.(map[string]any)
			if !ok || !exactKeys(check, "path", "equals") {
				return fmt.Errorf("%s: malformed secret resolveWhen", path)
			}
			target := stringValue(check["path"])
			targetNode, err := s.nodeAtPath(target)
			if err != nil || targetNode["type"] != "boolean" {
				return fmt.Errorf("%s: resolveWhen path must target a boolean leaf", path)
			}
			if _, ok := check["equals"].(bool); !ok || target == path {
				return fmt.Errorf("%s: malformed secret resolveWhen", path)
			}
		}
	}
	if sensitive, ok := node["x-vm-sensitive"].(string); ok &&
		!contains([]string{"reference", "credential", "path"}, sensitive) {
		return fmt.Errorf("%s: invalid x-vm-sensitive", path)
	}
	if integration, ok := node["x-vm-integration"]; ok {
		if kind != "object" || !strings.HasPrefix(path, "integrations.") {
			return fmt.Errorf("%s: x-vm-integration is only valid below integrations", path)
		}
		meta, ok := integration.(map[string]any)
		if !ok || !exactKeys(meta, "external", "egressHosts", "capabilities") {
			return fmt.Errorf("%s: malformed x-vm-integration", path)
		}
		external, externalOK := meta["external"].(bool)
		hosts, hostsOK := stringSlice(meta["egressHosts"])
		capabilities, capabilitiesOK := stringSlice(meta["capabilities"])
		if !externalOK || !hostsOK || !capabilitiesOK || hasDuplicateStrings(hosts) || hasDuplicateStrings(capabilities) {
			return fmt.Errorf("%s: malformed x-vm-integration", path)
		}
		for _, host := range hosts {
			parsed, err := url.Parse(host)
			if err != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" ||
				(external && parsed.Scheme != "https") {
				return fmt.Errorf("%s: invalid integration egress host", path)
			}
		}
		for _, capability := range capabilities {
			if matched, _ := regexp.MatchString(`^[a-z][a-z0-9-]{0,47}$`, capability); !matched {
				return fmt.Errorf("%s: invalid integration capability", path)
			}
		}
	}
	if kind == "object" {
		if node["additionalProperties"] != false {
			return fmt.Errorf("%s: object must set additionalProperties:false", path)
		}
		props, ok := node["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: object missing properties", path)
		}
		required, ok := stringSlice(node["required"])
		if !ok {
			return fmt.Errorf("%s: object missing required", path)
		}
		requiredSet := map[string]bool{}
		for _, key := range required {
			if requiredSet[key] {
				return fmt.Errorf("%s: duplicate required property %s", path, key)
			}
			requiredSet[key] = true
		}
		orders := map[int64]string{}
		docOrders := map[int64]string{}
		for key, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok || !requiredSet[key] {
				return fmt.Errorf("%s.%s: property must be required", path, key)
			}
			if _, ok := child["default"]; !ok {
				return fmt.Errorf("%s.%s: property missing default", path, key)
			}
			ui, _ := child["x-vm-ui"].(map[string]any)
			order, orderOK := integerValue(ui["order"])
			if orderOK {
				if prior := orders[order]; prior != "" {
					return fmt.Errorf("%s: duplicate UI order %d for %s and %s", path, order, prior, key)
				}
				orders[order] = key
			}
			doc, _ := child["x-vm-doc"].(map[string]any)
			docOrder, docOrderOK := integerValue(doc["order"])
			if docOrderOK {
				if prior := docOrders[docOrder]; prior != "" {
					return fmt.Errorf("%s: duplicate documentation order %d for %s and %s", path, docOrder, prior, key)
				}
				docOrders[docOrder] = key
			}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if err := s.metaNode(child, childPath, envPaths, refs); err != nil {
				return err
			}
		}
		if len(requiredSet) != len(props) {
			return fmt.Errorf("%s: required must list exactly every property", path)
		}
	}
	if kind == "array" {
		items, ok := node["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: array missing items schema", path)
		}
		if err := s.metaNode(items, path+"[]", envPaths, refs); err != nil {
			return err
		}
		ui, _ := node["x-vm-ui"].(map[string]any)
		if stringValue(ui["component"]) != "vm-string-list" || items["type"] != "string" {
			return fmt.Errorf("%s: string arrays require vm-string-list", path)
		}
	}
	if def, ok := node["default"]; ok {
		candidate := deepCopy(def)
		if kind == "object" {
			candidate = defaultsFor(node)
		}
		if issues := s.validateNode(node, candidate, path, nil); len(issues) > 0 {
			return fmt.Errorf("%s: invalid default: %s", path, issues[0].Message)
		}
	}
	return nil
}

func validateKeywordShapes(node map[string]any, kind, path string) error {
	for _, name := range []string{"title", "description"} {
		if value, exists := node[name]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s: %s must be a string", path, name)
			}
		}
	}
	rootOnly := []string{"$schema", "$id", "$defs"}
	for _, name := range rootOnly {
		if _, exists := node[name]; exists && path != "" {
			return fmt.Errorf("%s: %s is only valid at the schema root", path, name)
		}
	}
	if defs, exists := node["$defs"]; exists {
		if _, ok := defs.(map[string]any); !ok {
			return fmt.Errorf("%s: $defs must be an object", path)
		}
	}
	applicability := map[string]string{
		"properties": "object", "required": "object", "additionalProperties": "object",
		"items": "array", "uniqueItems": "array",
		"minLength": "string", "maxLength": "string", "pattern": "string",
	}
	for keyword, requiredKind := range applicability {
		if _, exists := node[keyword]; exists && kind != requiredKind {
			return fmt.Errorf("%s: %s is not valid for type %s", path, keyword, kind)
		}
	}
	for _, keyword := range []string{"minimum", "maximum"} {
		if value, exists := node[keyword]; exists {
			if kind != "integer" && kind != "number" {
				return fmt.Errorf("%s: %s is not valid for type %s", path, keyword, kind)
			}
			number, ok := numberValue(value)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
				return fmt.Errorf("%s: %s must be a finite number", path, keyword)
			}
		}
	}
	if minimum, minimumOK := numberValue(node["minimum"]); minimumOK {
		if maximum, maximumOK := numberValue(node["maximum"]); maximumOK && minimum > maximum {
			return fmt.Errorf("%s: minimum exceeds maximum", path)
		}
	}
	for _, keyword := range []string{"minLength", "maxLength"} {
		if value, exists := node[keyword]; exists {
			length, ok := integerValue(value)
			if !ok || length < 0 {
				return fmt.Errorf("%s: %s must be a non-negative integer", path, keyword)
			}
		}
	}
	if minimum, minimumOK := integerValue(node["minLength"]); minimumOK {
		if maximum, maximumOK := integerValue(node["maxLength"]); maximumOK && minimum > maximum {
			return fmt.Errorf("%s: minLength exceeds maxLength", path)
		}
	}
	if pattern, exists := node["pattern"]; exists {
		if _, ok := pattern.(string); !ok {
			return fmt.Errorf("%s: pattern must be a string", path)
		}
	}
	if choices, exists := node["enum"]; exists {
		values, ok := choices.([]any)
		if !ok || len(values) == 0 {
			return fmt.Errorf("%s: enum must be a non-empty array", path)
		}
		seen := map[string]bool{}
		for _, value := range values {
			encoded, err := json.Marshal(normalizeNumber(value))
			if err != nil || seen[string(encoded)] {
				return fmt.Errorf("%s: enum values must be valid and unique", path)
			}
			seen[string(encoded)] = true
			if issues := (&Schema{}).validateNode(map[string]any{"type": kind}, value, path, nil); len(issues) > 0 {
				return fmt.Errorf("%s: enum value has the wrong type", path)
			}
		}
	}
	if value, exists := node["uniqueItems"]; exists && value != true {
		return fmt.Errorf("%s: uniqueItems must be true", path)
	}
	if value, exists := node["x-vm-env"]; exists {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: x-vm-env must be a string", path)
		}
	}
	if value, exists := node["x-vm-sensitive"]; exists {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: x-vm-sensitive must be a string", path)
		}
	}
	if value, exists := node["x-vm-restart"]; exists {
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: x-vm-restart must be a string", path)
		}
	}
	return nil
}

func validateDoc(raw, enumRaw any, path string) error {
	doc, ok := raw.(map[string]any)
	enum := anyArray(enumRaw)
	keys := []string{"overview", "details", "tradeoffs", "examples", "links", "order"}
	if len(enum) > 0 {
		keys = append(keys, "choices")
	}
	if !ok || !exactKeys(doc, keys...) {
		return fmt.Errorf("%s: malformed x-vm-doc", path)
	}
	if strings.TrimSpace(stringValue(doc["overview"])) == "" {
		return fmt.Errorf("%s: empty x-vm-doc overview", path)
	}
	order, ok := integerValue(doc["order"])
	if !ok || order < 0 {
		return fmt.Errorf("%s: invalid x-vm-doc order", path)
	}
	details, ok := doc["details"].([]any)
	if !ok {
		return fmt.Errorf("%s: x-vm-doc details must be array", path)
	}
	for _, detail := range details {
		if strings.TrimSpace(stringValue(detail)) == "" {
			return fmt.Errorf("%s: x-vm-doc detail must be non-empty string", path)
		}
	}
	shapes := []struct {
		name string
		keys []string
	}{
		{"tradeoffs", []string{"choice", "benefit", "cost"}},
		{"examples", []string{"label", "yaml"}},
		{"links", []string{"label", "href"}},
	}
	for _, shape := range shapes {
		items, ok := doc[shape.name].([]any)
		if !ok {
			return fmt.Errorf("%s: x-vm-doc %s must be array", path, shape.name)
		}
		for _, item := range items {
			object, ok := item.(map[string]any)
			if !ok || !exactKeys(object, shape.keys...) {
				return fmt.Errorf("%s: malformed x-vm-doc %s item", path, shape.name)
			}
			for _, key := range shape.keys {
				if strings.TrimSpace(stringValue(object[key])) == "" && !(shape.name == "examples" && key == "yaml") {
					return fmt.Errorf("%s: empty x-vm-doc %s %s", path, shape.name, key)
				}
			}
			if shape.name == "links" {
				href := stringValue(object["href"])
				if strings.HasPrefix(strings.ToLower(href), "javascript:") ||
					!(strings.HasPrefix(href, "https://") || strings.HasPrefix(href, "../") || strings.HasPrefix(href, "./")) {
					return fmt.Errorf("%s: invalid x-vm-doc link", path)
				}
			}
		}
	}
	if len(enum) > 0 {
		choices, ok := doc["choices"].([]any)
		if !ok || len(choices) != len(enum) {
			return fmt.Errorf("%s: x-vm-doc choices must explain every enum value", path)
		}
		for index, rawChoice := range choices {
			choice, ok := rawChoice.(map[string]any)
			if !ok || !exactKeys(choice, "value", "description") ||
				!reflect.DeepEqual(normalizeTree(choice["value"]), normalizeTree(enum[index])) ||
				strings.TrimSpace(stringValue(choice["description"])) == "" {
				return fmt.Errorf("%s: malformed x-vm-doc choice at index %d", path, index)
			}
		}
	}
	return nil
}

func validateUI(raw any, kind, path string) error {
	ui, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: missing x-vm-ui", path)
	}
	if kind == "object" && ui["sectionRenderer"] != nil {
		if !exactKeys(ui, "section", "sectionRenderer", "order", "advanced") {
			return fmt.Errorf("%s: malformed section x-vm-ui", path)
		}
		renderer := stringValue(ui["sectionRenderer"])
		if !contains([]string{"vm-config-network-section", "vm-config-service-section",
			"vm-config-inference-section", "vm-config-agent-section", "vm-config-mail-section",
			"vm-config-health-section", "vm-config-integrations-section"}, renderer) {
			return fmt.Errorf("%s: invalid sectionRenderer", path)
		}
	} else {
		allowed := exactKeys(ui, "section", "component", "control", "order", "advanced") ||
			exactKeys(ui, "section", "component", "control", "order", "advanced", "hidden")
		if !allowed {
			return fmt.Errorf("%s: malformed leaf x-vm-ui", path)
		}
		if hidden, ok := ui["hidden"].(bool); ok && hidden {
			return nil
		}
		component := stringValue(ui["component"])
		control := stringValue(ui["control"])
		components := map[string]string{
			"vm-text-field": "text", "vm-number-field": "number", "vm-checkbox": "checkbox",
			"vm-select": "select", "vm-secret-reference": "secret", "vm-path-field": "path",
			"vm-address-field": "address", "vm-readonly-field": "readonly", "vm-string-list": "string-list",
		}
		if components[component] != control {
			return fmt.Errorf("%s: incompatible UI component/control", path)
		}
		switch control {
		case "number":
			if kind != "integer" && kind != "number" {
				return fmt.Errorf("%s: incompatible UI type", path)
			}
		case "checkbox":
			if kind != "boolean" {
				return fmt.Errorf("%s: incompatible UI type", path)
			}
		case "string-list":
			if kind != "array" {
				return fmt.Errorf("%s: incompatible UI type", path)
			}
		case "select", "readonly":
			// Select choices are provided by enum and checked after metadata.
		default:
			if kind != "string" && kind != "object" {
				return fmt.Errorf("%s: incompatible UI type", path)
			}
		}
	}
	if strings.TrimSpace(stringValue(ui["section"])) == "" {
		return fmt.Errorf("%s: x-vm-ui section must be non-empty", path)
	}
	order, orderOK := integerValue(ui["order"])
	if !orderOK || order < 0 {
		return fmt.Errorf("%s: invalid x-vm-ui order", path)
	}
	if _, ok := ui["advanced"].(bool); !ok {
		return fmt.Errorf("%s: x-vm-ui advanced must be boolean", path)
	}
	return nil
}

func (s *Schema) nodeAtPath(dotted string) (map[string]any, error) {
	node := s.root
	for _, part := range strings.Split(dotted, ".") {
		properties, _ := node["properties"].(map[string]any)
		next, ok := properties[part].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown schema path %s", dotted)
		}
		node = next
	}
	return node, nil
}

func (s *Schema) SecretTTL(dotted string) (time.Duration, error) {
	node, err := s.nodeAtPath(dotted)
	if err != nil {
		return 0, err
	}
	secret, ok := node["x-vm-secret"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("%s is not a secret field", dotted)
	}
	seconds, ok := integerValue(secret["cacheTtlSeconds"])
	if !ok {
		return 0, fmt.Errorf("%s has invalid secret TTL", dotted)
	}
	return time.Duration(seconds) * time.Second, nil
}

func (s *Schema) validateExamples() error {
	defaults, err := s.Defaults()
	if err != nil {
		return err
	}
	var visit func(map[string]any, string) error
	visit = func(node map[string]any, path string) error {
		if doc, ok := node["x-vm-doc"].(map[string]any); ok {
			for _, raw := range anyArray(doc["examples"]) {
				example, _ := raw.(map[string]any)
				yaml := stringValue(example["yaml"])
				overlay, _, err := ParseYAML([]byte(yaml))
				if err != nil {
					return fmt.Errorf("%s: invalid example YAML: %w", path, err)
				}
				candidate := deepCopy(defaults).(map[string]any)
				if err := overlayConfig(candidate, overlay, "", map[string]Source{}); err != nil {
					return fmt.Errorf("%s: invalid example overlay: %w", path, err)
				}
				if err := s.Validate(candidate); err != nil {
					return fmt.Errorf("%s: example does not validate: %w", path, err)
				}
			}
		}
		properties, _ := node["properties"].(map[string]any)
		for key, raw := range properties {
			if err := visit(raw.(map[string]any), joinPath(path, key)); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(s.root, "")
}

func (s *Schema) validateSecretConditions() error {
	edges := map[string]string{}
	var walk func(map[string]any, string)
	walk = func(node map[string]any, path string) {
		if secret, ok := node["x-vm-secret"].(map[string]any); ok {
			if condition, ok := secret["resolveWhen"].(map[string]any); ok {
				edges[path] = stringValue(condition["path"])
			}
		}
		properties, _ := node["properties"].(map[string]any)
		for key, raw := range properties {
			walk(raw.(map[string]any), joinPath(path, key))
		}
	}
	walk(s.root, "")
	for start := range edges {
		seen := map[string]bool{}
		for current := start; edges[current] != ""; current = edges[current] {
			if seen[current] {
				return fmt.Errorf("%s: cyclic secret resolveWhen", start)
			}
			seen[current] = true
		}
	}
	return nil
}

func hasDuplicateStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func (s *Schema) resolveRef(ref string) (map[string]any, error) {
	defs, _ := s.root["$defs"].(map[string]any)
	target, ok := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unresolved reference %s", ref)
	}
	return target, nil
}

func (s *Schema) Defaults() (RawConfig, error) {
	value := defaultsFor(s.root)
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("root default is not an object")
	}
	if err := s.Validate(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func defaultsFor(node map[string]any) any {
	if node["type"] == "object" {
		result := map[string]any{}
		if base, ok := node["default"].(map[string]any); ok {
			for key, value := range base {
				result[key] = deepCopy(value)
			}
		}
		props, _ := node["properties"].(map[string]any)
		for key, raw := range props {
			result[key] = defaultsFor(raw.(map[string]any))
		}
		return result
	}
	return deepCopy(node["default"])
}

type Issue struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "configuration is invalid"
	}
	return e.Issues[0].Path + ": " + e.Issues[0].Message
}

func (s *Schema) Validate(value any) error {
	if raw, ok := value.(RawConfig); ok {
		value = map[string]any(raw)
	}
	issues := s.validateNode(s.root, value, "", nil)
	if root, ok := value.(map[string]any); ok {
		if integrations, ok := root["integrations"].(map[string]any); ok {
			if telegram, ok := integrations["telegram"].(map[string]any); ok {
				enabled, _ := telegram["enabled"].(bool)
				token, _ := telegram["botToken"].(string)
				chats, _ := telegram["allowedChatIds"].([]any)
				if token != "" && envReferencePattern.FindStringSubmatch(token) == nil &&
					fileReferencePattern.FindStringSubmatch(token) == nil &&
					dataFileReferencePattern.FindStringSubmatch(token) == nil {
					issues = append(issues, Issue{Path: "integrations.telegram.botToken", Message: "must be an exact environment or file secret reference"})
				}
				if enabled && token == "" {
					issues = append(issues, Issue{Path: "integrations.telegram.botToken", Message: "secret reference is required while Telegram is enabled"})
				}
				if enabled && len(chats) == 0 {
					issues = append(issues, Issue{Path: "integrations.telegram.allowedChatIds", Message: "at least one chat ID is required while Telegram is enabled"})
				}
			}
		}
	}
	if len(issues) > 0 {
		if len(issues) > 20 {
			omitted := len(issues) - 20
			issues = append(append([]Issue(nil), issues[:20]...), Issue{
				Message: fmt.Sprintf("%d additional validation issues omitted", omitted),
			})
		}
		for index := range issues {
			if issues[index].Hint == "" && issues[index].Path != "" {
				issues[index].Hint = "see /config#" + anchorFor(issues[index].Path)
			}
		}
		return &ValidationError{Issues: issues}
	}
	return nil
}

func (s *Schema) validateNode(node map[string]any, value any, path string, issues []Issue) []Issue {
	if ref, ok := node["$ref"].(string); ok {
		target, err := s.resolveRef(ref)
		if err != nil {
			return append(issues, Issue{Path: path, Message: err.Error()})
		}
		node = target
	}
	kind := stringValue(node["type"])
	validType := false
	switch kind {
	case "object":
		_, validType = value.(map[string]any)
	case "array":
		_, validType = value.([]any)
	case "string":
		_, validType = value.(string)
	case "boolean":
		_, validType = value.(bool)
	case "null":
		validType = value == nil
	case "integer":
		_, validType = integerValue(value)
	case "number":
		_, validType = numberValue(value)
	}
	if !validType {
		return append(issues, Issue{Path: path, Message: fmt.Sprintf("expected %s, got %s", kind, describeActual(node, value))})
	}
	if expected, ok := node["const"]; ok && !reflect.DeepEqual(normalizeNumber(value), normalizeNumber(expected)) {
		issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("must equal %v", expected)})
	}
	if choices, ok := node["enum"].([]any); ok {
		found := false
		for _, choice := range choices {
			found = found || reflect.DeepEqual(normalizeNumber(value), normalizeNumber(choice))
		}
		if !found {
			issues = append(issues, Issue{Path: path, Message: "value is not an allowed choice"})
		}
	}
	if text, ok := value.(string); ok {
		if minimum, ok := integerValue(node["minLength"]); ok && int64(len([]rune(text))) < minimum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("minimum length is %d", minimum)})
		}
		if maximum, ok := integerValue(node["maxLength"]); ok && int64(len([]rune(text))) > maximum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("maximum length is %d", maximum)})
		}
		if pattern, ok := node["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(text) {
			issues = append(issues, Issue{Path: path, Message: "does not match required pattern"})
		}
	}
	if number, ok := numberValue(value); ok {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			issues = append(issues, Issue{Path: path, Message: "number must be finite"})
		}
		if minimum, ok := numberValue(node["minimum"]); ok && number < minimum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("minimum is %v", minimum)})
		}
		if maximum, ok := numberValue(node["maximum"]); ok && number > maximum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("maximum is %v", maximum)})
		}
	}
	if object, ok := value.(map[string]any); ok {
		props, _ := node["properties"].(map[string]any)
		required, _ := stringSlice(node["required"])
		for _, key := range required {
			if _, exists := object[key]; !exists {
				issues = append(issues, Issue{Path: joinPath(path, key), Message: "required value is missing"})
			}
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			rawSchema, exists := props[key]
			if !exists {
				issues = append(issues, Issue{Path: joinPath(path, key), Message: "unknown configuration key"})
				continue
			}
			issues = s.validateNode(rawSchema.(map[string]any), object[key], joinPath(path, key), issues)
		}
	}
	if array, ok := value.([]any); ok {
		itemSchema, _ := node["items"].(map[string]any)
		for index, item := range array {
			issues = s.validateNode(itemSchema, item, fmt.Sprintf("%s[%d]", path, index), issues)
		}
		if node["uniqueItems"] == true {
			for i := range array {
				for j := 0; j < i; j++ {
					if reflect.DeepEqual(normalizeNumber(array[i]), normalizeNumber(array[j])) {
						issues = append(issues, Issue{Path: fmt.Sprintf("%s[%d]", path, i), Message: "duplicate array item"})
					}
				}
			}
		}
	}
	return issues
}

func describeActual(node map[string]any, value any) string {
	if _, secret := node["x-vm-secret"]; secret {
		return "<secret reference>"
	}
	switch typed := value.(type) {
	case string:
		if len(typed) > 80 {
			return "string " + strconv.Quote(typed[:77]+"...")
		}
		return "string " + strconv.Quote(typed)
	case nil:
		return "null"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any, RawConfig:
		return "object"
	default:
		if _, ok := integerValue(value); ok {
			return "integer"
		}
		if _, ok := numberValue(value); ok {
			return "number"
		}
		return fmt.Sprintf("%T", value)
	}
}

func deepCopy(value any) any {
	switch typed := value.(type) {
	case RawConfig:
		copy := make(map[string]any, len(typed))
		for key, item := range typed {
			copy[key] = deepCopy(item)
		}
		return copy
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, item := range typed {
			copy[key] = deepCopy(item)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, item := range typed {
			copy[index] = deepCopy(item)
		}
		return copy
	default:
		return typed
	}
}

func cloneBoolMap(value map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(value))
	for key, item := range value {
		copy[key] = item
	}
	return copy
}

func exactKeys(value map[string]any, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func stringSlice(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(items))
	for index, item := range items {
		result[index], ok = item.(string)
		if !ok {
			return nil, false
		}
	}
	return result, true
}

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case json.Number:
		number, err := typed.Int64()
		return number, err == nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) ||
			typed < math.MinInt64 || typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	default:
		return 0, false
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func normalizeNumber(value any) any {
	if number, ok := integerValue(value); ok {
		return number
	}
	return value
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
