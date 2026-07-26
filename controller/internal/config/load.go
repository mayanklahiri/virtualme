package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

const FileName = "virtualme.config.yaml"

type Options struct {
	DataDir  string
	Env      []string
	Warn     func(string)
	Resolver *Resolver
}

func Load(options Options) (*Loaded, error) {
	schema, err := EmbeddedSchema()
	if err != nil {
		return nil, err
	}
	root := options.DataDir
	if root == "" {
		root = environmentMap(options.Env)["VM_DATA_DIR"]
	}
	if root == "" {
		root = "/home/virtualme/.virtualme"
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("config bootstrap data directory must be clean and absolute")
	}
	file := filepath.Join(root, FileName)
	defaults, err := schema.Defaults()
	if err != nil {
		return nil, err
	}
	if err := seedIfAbsent(schema, defaults, file); err != nil {
		return nil, err
	}
	input, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	overlay, locations, err := ParseYAML(input)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	raw := deepCopy(defaults).(map[string]any)
	sources := map[string]Source{}
	markSources(schema.root, "", SourceDefault, sources)
	if err := overlayConfig(raw, overlay, "", sources); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	if err := schema.Validate(deferredValidationTree(schema.root, raw).(map[string]any)); err != nil {
		return nil, withLocations(file, err, locations)
	}
	effective := deepCopy(raw).(map[string]any)
	environment := options.Env
	if environment == nil {
		environment = os.Environ()
	}
	env := environmentMap(environment)
	if err := applyLegacy(schema.root, effective, "", env, sources, options.Warn, overlay); err != nil {
		return nil, fmt.Errorf("config %s: %w", file, err)
	}
	resolver := options.Resolver
	ownedResolver := false
	if resolver == nil {
		resolver, ownedResolver = NewResolver(environment, root), true
	}
	if ownedResolver {
		defer resolver.Close()
	}
	secrets := map[string]SecretStatus{}
	if err := resolveTree(schema.root, effective, raw, "", root, env, resolver, secrets, effective); err != nil {
		return nil, withLocations(file, err, locations)
	}
	if err := schema.Validate(resolvedValidationTree(schema.root, effective, raw)); err != nil {
		return nil, withLocations(file, err, locations)
	}
	if err := ValidateSemantic(effective); err != nil {
		return nil, withLocations(file, err, locations)
	}
	var typed Config
	encoded, _ := json.Marshal(effective)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&typed); err != nil {
		return nil, fmt.Errorf("config typed decode: %w", err)
	}
	canonical, err := schema.Emit(raw)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	return &Loaded{
		Config: typed, Raw: raw, Sources: sources, Secrets: secrets,
		Hash: hex.EncodeToString(sum[:]), SchemaHash: schema.Hash(),
		DataDir: root, File: file,
	}, nil
}

func deferredValidationTree(schema map[string]any, value any) any {
	if schema["type"] == "object" {
		object, ok := value.(map[string]any)
		if !ok {
			if raw, rawOK := value.(RawConfig); rawOK {
				object, ok = map[string]any(raw), true
			}
		}
		if !ok {
			return value
		}
		result := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		for key, item := range object {
			child, ok := props[key].(map[string]any)
			if !ok {
				result[key] = item
			} else {
				result[key] = deferredValidationTree(child, item)
			}
		}
		return result
	}
	if text, ok := value.(string); ok && envReferencePattern.MatchString(text) {
		if _, secret := schema["x-vm-secret"]; secret {
			return value
		}
		return deepCopy(schema["default"])
	}
	return value
}

func seedIfAbsent(schema *Schema, defaults RawConfig, file string) error {
	if _, err := os.Lstat(file); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	lock := file + ".lock"
	lockFile, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("config first-start lock %s: %w", lock, err)
	}
	_ = lockFile.Close()
	defer os.Remove(lock)
	if _, err := os.Lstat(file); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content, err := schema.Emit(defaults)
	if err != nil {
		return err
	}
	return AtomicWrite(file, content)
}

func overlayConfig(target, overlay map[string]any, path string, sources map[string]Source) error {
	for key, value := range overlay {
		childPath := joinPath(path, key)
		if nested, ok := value.(map[string]any); ok {
			base, exists := target[key].(map[string]any)
			if !exists {
				target[key] = deepCopy(nested)
				markValueSources(nested, childPath, SourceYAML, sources)
				continue
			}
			if err := overlayConfig(base, nested, childPath, sources); err != nil {
				return err
			}
		} else {
			target[key] = deepCopy(value)
			sources[childPath] = SourceYAML
		}
	}
	return nil
}

func markSources(schema map[string]any, path string, source Source, result map[string]Source) {
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		childPath := joinPath(path, key)
		child := raw.(map[string]any)
		if child["type"] == "object" {
			markSources(child, childPath, source, result)
		} else {
			result[childPath] = source
		}
	}
}

func markValueSources(value map[string]any, path string, source Source, result map[string]Source) {
	for key, item := range value {
		childPath := joinPath(path, key)
		if object, ok := item.(map[string]any); ok {
			markValueSources(object, childPath, source, result)
		} else {
			result[childPath] = source
		}
	}
}

func applyLegacy(schema map[string]any, effective map[string]any, path string, env map[string]string,
	sources map[string]Source, warn func(string), yamlOverlay map[string]any) error {
	return applyLegacyNode(schema, effective, path, env, sources, warn, yamlOverlay, schema, effective, yamlOverlay)
}

func applyLegacyNode(schema map[string]any, effective map[string]any, path string, env map[string]string,
	sources map[string]Source, warn func(string), yamlOverlay, rootSchema, rootEffective, rootOverlay map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for key, raw := range props {
		child := raw.(map[string]any)
		childPath := joinPath(path, key)
		if child["type"] == "object" {
			if err := applyLegacyNode(child, effective[key].(map[string]any), childPath, env, sources, warn,
				nestedMap(yamlOverlay[key]), rootSchema, rootEffective, rootOverlay); err != nil {
				return err
			}
			continue
		}
		name, _ := child["x-vm-env"].(string)
		value, present := env[name]
		if !present || name == "" {
			continue
		}
		if value == "" && child["type"] != "string" {
			return fmt.Errorf("legacy %s for %s is empty", name, childPath)
		}
		var converted any
		var err error
		if (name == "VM_LLAMA_PORT" && childPath == "llama.address") ||
			(name == "VM_TTS_PORT" && childPath == "tts.address") {
			port, parseErr := strconv.Atoi(value)
			if parseErr != nil || port < 1 || port > 65535 {
				err = fmt.Errorf("must be decimal port 1..65535")
			} else {
				converted = "127.0.0.1:" + strconv.Itoa(port)
				derivePortURLs(rootSchema, rootEffective, childPath, port, rootOverlay, env)
			}
		} else if name == "VM_MAIL_SMARTHOST_PASS" {
			converted = value
		} else {
			converted, err = convertString(value, stringValue(child["type"]), true)
		}
		if err != nil {
			return fmt.Errorf("legacy %s for %s: %w", name, childPath, err)
		}
		effective[key] = converted
		sources[childPath] = SourceLegacy
		if warn != nil {
			if name == "VM_MAIL_SMARTHOST_PASS" {
				warn(fmt.Sprintf("config: legacy %s overrides %s; migrate it to ${env:...} or ${file:...}", name, childPath))
			} else {
				warn(fmt.Sprintf("config: legacy %s overrides %s; migrate it to virtualme.config.yaml", name, childPath))
			}
		}
	}
	return nil
}

func derivePortURLs(schema, effective map[string]any, path string, port int, overlay map[string]any, env map[string]string) {
	if path == "llama.address" {
		llama := effective["llama"].(map[string]any)
		health := effective["health"].(map[string]any)
		yamlLlama := nestedMap(overlay["llama"])
		yamlHealth := nestedMap(overlay["health"])
		if valueIsAbsentOrDefault(schema, "llama.chatCompletionsURL", yamlLlama["chatCompletionsURL"]) {
			llama["chatCompletionsURL"] = fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port)
		}
		if valueIsAbsentOrDefault(schema, "health.llamaURL", yamlHealth["llamaURL"]) && env["VM_LLAMA_HEALTH_URL"] == "" {
			health["llamaURL"] = fmt.Sprintf("http://127.0.0.1:%d/health", port)
		}
	}
	if path == "tts.address" {
		tts := effective["tts"].(map[string]any)
		yamlTTS := nestedMap(overlay["tts"])
		if valueIsAbsentOrDefault(schema, "tts.healthURL", yamlTTS["healthURL"]) && env["VM_TTS_HEALTH_URL"] == "" {
			tts["healthURL"] = fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		}
	}
}

func valueIsAbsentOrDefault(schema map[string]any, dotted string, value any) bool {
	node := schema
	for _, part := range strings.Split(dotted, ".") {
		properties, _ := node["properties"].(map[string]any)
		node, _ = properties[part].(map[string]any)
		if node == nil {
			return false
		}
	}
	return value == nil || reflect.DeepEqual(normalizeNumber(value), normalizeNumber(node["default"]))
}

func resolveTree(schema map[string]any, effective, raw map[string]any, path, dataRoot string,
	env map[string]string, resolver *Resolver, secrets map[string]SecretStatus, rootEffective map[string]any) error {
	if err := resolveNonSecrets(schema, effective, path, dataRoot, env); err != nil {
		return err
	}
	return resolveSecrets(schema, effective, raw, path, resolver, secrets, rootEffective)
}

func resolveNonSecrets(schema map[string]any, effective map[string]any, path, dataRoot string,
	env map[string]string) error {
	props, _ := schema["properties"].(map[string]any)
	for key, rawSchema := range props {
		child := rawSchema.(map[string]any)
		childPath := joinPath(path, key)
		if child["type"] == "object" {
			if err := resolveNonSecrets(child, effective[key].(map[string]any), childPath, dataRoot, env); err != nil {
				return err
			}
			continue
		}
		if child["type"] == "array" {
			if containsReference(effective[key]) {
				return validationIssue(childPath, "interpolation is not allowed inside arrays")
			}
			continue
		}
		if _, secret := child["x-vm-secret"]; secret {
			continue
		}
		value := effective[key]
		if text, ok := value.(string); ok && strings.HasPrefix(text, "${data}") {
			if sourcesTokenAllowed(childPath, text) {
				value = filepath.Join(dataRoot, strings.TrimPrefix(text, "${data}/"))
				effective[key] = value
			} else {
				return validationIssue(childPath, "${data} is only valid in documented defaults")
			}
		}
		if text, ok := value.(string); ok {
			if strings.Contains(text, "${") {
				match := envReferencePattern.FindStringSubmatch(text)
				if match == nil {
					return validationIssue(childPath, "interpolation must be one whole ${env:NAME} scalar")
				}
				envValue, present := env[match[1]]
				if !present {
					return validationIssue(childPath, fmt.Sprintf("environment %s is unavailable", match[1]))
				}
				converted, err := convertString(envValue, stringValue(child["type"]), false)
				if err != nil {
					return validationIssue(childPath, fmt.Sprintf("environment %s: %v", match[1], err))
				}
				effective[key] = converted
			}
		}
	}
	return nil
}

func resolveSecrets(schema map[string]any, effective, raw map[string]any, path string,
	resolver *Resolver, secrets map[string]SecretStatus, rootEffective map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for key, rawSchema := range props {
		child := rawSchema.(map[string]any)
		childPath := joinPath(path, key)
		if child["type"] == "object" {
			if err := resolveSecrets(child, effective[key].(map[string]any), raw[key].(map[string]any),
				childPath, resolver, secrets, rootEffective); err != nil {
				return err
			}
			continue
		}
		secretMeta, secret := child["x-vm-secret"].(map[string]any)
		if !secret {
			continue
		}
		rawValue := raw[key]
		value := effective[key]
		text, _ := value.(string)
		if text == "" {
			secrets[childPath] = SecretStatus{Configured: false, Error: ""}
			continue
		}
		if sourcesValue := stringValueFromAny(rawValue); sourcesValue != text && sourcesValue != "" {
			text = sourcesValue
		}
		if envReferencePattern.FindStringSubmatch(text) == nil &&
			fileReferencePattern.FindStringSubmatch(text) == nil &&
			dataFileReferencePattern.FindStringSubmatch(text) == nil {
			if sourcesValue := stringValueFromAny(rawValue); sourcesValue == "" && effective[key] != "" {
				// Temporary literal legacy adapter; effective only.
				secrets[childPath] = SecretStatus{Configured: true, Resolved: true, Source: "legacy-env", Error: ""}
				continue
			}
			return validationIssue(childPath, "secret must be empty or a whole ${env:...}, ${file:/...}, or ${file:${data}/...} reference")
		}
		if condition, ok := secretMeta["resolveWhen"].(map[string]any); ok {
			expected, _ := condition["equals"].(bool)
			actual, actualOK := valueAtPath(rootEffective, stringValue(condition["path"])).(bool)
			if !actualOK || actual != expected {
				secrets[childPath] = SecretStatus{
					Reference: text, Configured: true, Resolved: false, Status: "inactive", Error: "",
				}
				effective[key] = ""
				continue
			}
		}
		ttl, _ := integerValue(secretMeta["cacheTtlSeconds"])
		bytes, status, err := resolver.Resolve(text, time.Duration(ttl)*time.Second)
		secrets[childPath] = status
		if err != nil {
			if childPath == "integrations.telegram.botToken" {
				effective[key] = ""
				continue
			}
			return validationIssue(childPath, status.Error)
		}
		effective[key] = string(bytes)
		zero(bytes)
	}
	return nil
}

func resolvedValidationTree(schema map[string]any, effective, raw any) any {
	if _, secret := schema["x-vm-secret"]; secret {
		return deepCopy(raw)
	}
	switch schema["type"] {
	case "object":
		result := map[string]any{}
		effectiveObject := objectValue(effective)
		rawObject := objectValue(raw)
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range properties {
			result[key] = resolvedValidationTree(child.(map[string]any), effectiveObject[key], rawObject[key])
		}
		return result
	default:
		return deepCopy(effective)
	}
}

func validationIssue(path, message string) error {
	return &ValidationError{Issues: []Issue{{
		Path: path, Message: message, Hint: "see /config#" + anchorFor(path),
	}}}
}

func valueAtPath(root map[string]any, dotted string) any {
	var value any = root
	for _, part := range strings.Split(dotted, ".") {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[part]
	}
	return value
}

func containsReference(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, "${")
	case []any:
		for _, item := range typed {
			if containsReference(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if containsReference(item) {
				return true
			}
		}
	}
	return false
}

func convertString(value, kind string, legacyBool bool) (any, error) {
	switch kind {
	case "string":
		return value, nil
	case "integer":
		if !integerPattern.MatchString(value) {
			return nil, errors.New("expected canonical base-10 integer")
		}
		return strconv.ParseInt(value, 10, 64)
	case "number":
		if !numberPattern.MatchString(value) && !integerPattern.MatchString(value) {
			return nil, errors.New("expected finite decimal without exponent")
		}
		return strconv.ParseFloat(value, 64)
	case "boolean":
		switch strings.ToLower(value) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "1":
			if legacyBool {
				return true, nil
			}
		case "0":
			if legacyBool {
				return false, nil
			}
		}
		return nil, errors.New("expected boolean true or false")
	case "null":
		if value == "null" {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("cannot convert to %s", kind)
}

func environmentMap(environment []string) map[string]string {
	result := map[string]string{}
	for _, item := range environment {
		if index := strings.IndexByte(item, '='); index >= 0 {
			result[item[:index]] = item[index+1:]
		}
	}
	return result
}

func nestedMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

func sourcesTokenAllowed(path, value string) bool {
	return (path == "tts.cacheDirectory" || path == "mail.spoolDirectory") &&
		strings.HasPrefix(value, "${data}/")
}

func stringValueFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func withLocations(file string, err error, locations map[string]Location) error {
	validation := new(ValidationError)
	if !errors.As(err, &validation) {
		return fmt.Errorf("config %s: %w", file, err)
	}
	copyIssues := append([]Issue(nil), validation.Issues...)
	for index := range copyIssues {
		location := locations[copyIssues[index].Path]
		copyIssues[index].Line, copyIssues[index].Column = location.Line, location.Column
	}
	return &FileError{File: file, Issues: copyIssues}
}

type FileError struct {
	File   string
	Issues []Issue
}

func (e *FileError) Error() string {
	if len(e.Issues) == 0 {
		return "config " + e.File + ": invalid"
	}
	issue := e.Issues[0]
	return fmt.Sprintf("config %s:%d:%d at %s: %s", e.File, issue.Line, issue.Column, issue.Path, issue.Message)
}

func ValidateSemantic(raw map[string]any) error {
	var issues []Issue
	add := func(path, message string) {
		issues = append(issues, Issue{
			Path: path, Message: message, Hint: "see /config#" + anchorFor(path),
		})
	}
	desktop := raw["desktop"].(map[string]any)
	var width, height, depth int
	if _, err := fmt.Sscanf(desktop["resolution"].(string), "%dx%dx%d", &width, &height, &depth); err != nil ||
		width < 320 || width > 16384 || height < 320 || height > 16384 {
		add("desktop.resolution", "width and height must be 320..16384")
	}
	if desktop["noVNCUpstreamAddress"] != desktop["vncAddress"] {
		add("desktop.noVNCUpstreamAddress", "must equal desktop.vncAddress")
	}
	server := raw["server"].(map[string]any)
	if authority(stringValue(server["desktopProxyURL"])) != stringValue(desktop["noVNCAddress"]) {
		add("server.desktopProxyURL", "authority must equal desktop.noVNCAddress")
	}
	llama := raw["llama"].(map[string]any)
	health := raw["health"].(map[string]any)
	if authority(stringValue(llama["chatCompletionsURL"])) != stringValue(llama["address"]) {
		add("llama.chatCompletionsURL", "authority must equal llama.address")
	}
	if authority(stringValue(health["llamaURL"])) != stringValue(llama["address"]) {
		add("health.llamaURL", "authority must equal llama.address")
	}
	tts := raw["tts"].(map[string]any)
	if authority(stringValue(tts["healthURL"])) != stringValue(tts["address"]) {
		add("tts.healthURL", "authority must equal tts.address")
	}
	addresses := []struct{ path, value string }{
		{"desktop.vncAddress", stringValue(desktop["vncAddress"])},
		{"desktop.noVNCAddress", stringValue(desktop["noVNCAddress"])},
		{"desktop.noVNCUpstreamAddress", stringValue(desktop["noVNCUpstreamAddress"])},
		{"valkey.address", stringValue(raw["valkey"].(map[string]any)["address"])},
		{"llama.address", stringValue(llama["address"])},
		{"tts.address", stringValue(tts["address"])},
	}
	for _, item := range addresses {
		if !loopbackAddress(item.value) {
			add(item.path, "must be a loopback host:port")
		}
	}
	urls := []struct{ path, value, requiredPath string }{
		{"server.desktopProxyURL", stringValue(server["desktopProxyURL"]), ""},
		{"desktop.noVNCHealthURL", stringValue(desktop["noVNCHealthURL"]), "/vnc.html"},
		{"desktop.cdpURL", stringValue(desktop["cdpURL"]), ""},
		{"llama.chatCompletionsURL", stringValue(llama["chatCompletionsURL"]), "/v1/chat/completions"},
		{"tts.healthURL", stringValue(tts["healthURL"]), "/healthz"},
		{"health.llamaURL", stringValue(health["llamaURL"]), "/health"},
	}
	for _, item := range urls {
		if !loopbackURL(item.value) {
			add(item.path, "must be a loopback HTTP URL")
		} else if parsed, _ := url.Parse(item.value); parsed.Path != item.requiredPath && !(item.requiredPath == "" && parsed.Path == "/") {
			add(item.path, "has an inappropriate URL path")
		}
	}
	if !validListenAddress(stringValue(server["httpAddress"])) {
		add("server.httpAddress", "must be a host:port address")
	}
	portOwners := []struct{ path, value string }{
		{"server.httpAddress", stringValue(server["httpAddress"])},
		{"desktop.vncAddress", stringValue(desktop["vncAddress"])},
		{"desktop.noVNCAddress", stringValue(desktop["noVNCAddress"])},
		{"desktop.cdpURL", authority(stringValue(desktop["cdpURL"]))},
		{"valkey.address", stringValue(raw["valkey"].(map[string]any)["address"])},
		{"llama.address", stringValue(llama["address"])},
		{"tts.address", stringValue(tts["address"])},
	}
	ports := map[string]string{}
	for _, item := range portOwners {
		_, port, err := net.SplitHostPort(item.value)
		if err != nil {
			continue
		}
		if previous := ports[port]; previous != "" {
			add(item.path, "port collides with "+previous)
		} else {
			ports[port] = item.path
		}
	}
	mail := raw["mail"].(map[string]any)
	smart := mail["smarthost"].(map[string]any)
	user, password, host := stringValue(smart["username"]), stringValue(smart["password"]), stringValue(smart["host"])
	if (user == "") != (password == "") {
		add("mail.smarthost", "username and password must both be empty or present")
	}
	if (user != "" || password != "") && host == "" {
		add("mail.smarthost.host", "host is required with credentials")
	}
	if host != "" && !validHostname(host) {
		add("mail.smarthost.host", "must be a valid hostname")
	}
	if domain := stringValue(mail["dkimDomain"]); domain != "" && !validHostname(domain) {
		add("mail.dkimDomain", "must be a valid hostname")
	}
	pathValues := []struct{ path, value string }{
		{"desktop.x11SocketDirectory", stringValue(desktop["x11SocketDirectory"])},
		{"llama.modelPath", stringValue(llama["modelPath"])},
		{"llama.projectorPath", stringValue(llama["projectorPath"])},
		{"tts.sherpaDirectory", stringValue(tts["sherpaDirectory"])},
		{"tts.modelDirectory", stringValue(tts["modelDirectory"])},
		{"tts.cacheDirectory", stringValue(tts["cacheDirectory"])},
		{"agent.systemManifestPath", stringValue(raw["agent"].(map[string]any)["systemManifestPath"])},
		{"mail.sendmailPath", stringValue(mail["sendmailPath"])},
		{"mail.spoolDirectory", stringValue(mail["spoolDirectory"])},
	}
	for _, item := range pathValues {
		if !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value {
			add(item.path, "must be a clean absolute path")
		}
	}
	agent := raw["agent"].(map[string]any)
	executables := []struct{ path, value string }{
		{"agent.xdotoolPath", stringValue(agent["xdotoolPath"])},
		{"agent.scrotPath", stringValue(agent["scrotPath"])},
		{"agent.convertPath", stringValue(agent["convertPath"])},
		{"agent.bashPath", stringValue(agent["bashPath"])},
		{"health.xdotoolPath", stringValue(health["xdotoolPath"])},
	}
	for _, item := range executables {
		if strings.Contains(item.value, "/") && (!filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value) {
			add(item.path, "must be an executable name or clean absolute path")
		}
	}
	walkStrings(raw, "", func(path, value string) {
		for _, char := range value {
			if char == 0 || char < 0x20 || char == 0x7f {
				add(path, "must not contain control characters")
				break
			}
		}
	})
	if len(issues) > 0 {
		return &ValidationError{Issues: issues}
	}
	return nil
}

func authority(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func loopbackURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && loopbackHost(parsed.Hostname())
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	if err != nil || !loopbackHost(host) {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}

func validListenAddress(value string) bool {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}

func loopbackHost(host string) bool {
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func validHostname(value string) bool {
	if len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' ||
				char >= '0' && char <= '9' || char == '-') {
				return false
			}
		}
	}
	return true
}

func walkStrings(value any, path string, visit func(string, string)) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkStrings(typed[key], joinPath(path, key), visit)
		}
	case []any:
		for index, item := range typed {
			walkStrings(item, fmt.Sprintf("%s[%d]", path, index), visit)
		}
	case string:
		visit(path, typed)
	}
}

// ClearLegacyTelegramUserAllowlist clears the deprecated
// integrations.telegram.allowedUserIds value after Valkey migration. The key
// remains present because the schema requires it.
func ClearLegacyTelegramUserAllowlist(loaded *Loaded) error {
	if loaded == nil || loaded.File == "" {
		return nil
	}
	integrations, ok := loaded.Raw["integrations"].(map[string]any)
	if !ok {
		return nil
	}
	telegram, ok := integrations["telegram"].(map[string]any)
	if !ok {
		return nil
	}
	legacy, ok := telegram["allowedUserIds"]
	if !ok {
		return nil
	}
	switch values := legacy.(type) {
	case []any:
		if len(values) == 0 {
			return nil
		}
	case []string:
		if len(values) == 0 {
			return nil
		}
	}
	telegram["allowedUserIds"] = []any{}
	schema, err := EmbeddedSchema()
	if err != nil {
		return err
	}
	content, err := schema.Emit(loaded.Raw)
	if err != nil {
		return err
	}
	return AtomicWrite(loaded.File, content)
}
