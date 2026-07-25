package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSchemaDefaultsAndCanonicalRoundTrip(t *testing.T) {
	schema, err := EmbeddedSchema()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schema.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	yaml, err := schema.Emit(raw)
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("testdata", "default.yaml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, yaml, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(yaml, golden) {
		t.Fatal("default YAML differs from testdata/default.yaml")
	}
	if !strings.Contains(string(yaml), "# Master configuration version.") ||
		!strings.Contains(string(yaml), "contextTokens: 32768") ||
		!strings.HasSuffix(string(yaml), "\n") {
		t.Fatalf("canonical defaults incomplete:\n%s", yaml)
	}
	parsed, _, err := ParseYAML(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(parsed); err != nil {
		t.Fatal(err)
	}
	reemitted, err := schema.Emit(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(yaml, reemitted) {
		t.Fatal("canonical YAML did not round-trip byte-for-byte")
	}
	var typed Config
	encoded, _ := json.Marshal(raw)
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&typed); err != nil {
		t.Fatalf("schema and Config differ: %v", err)
	}
	typedJSON, _ := json.Marshal(typed)
	var typedRaw map[string]any
	typedDecoder := json.NewDecoder(bytes.NewReader(typedJSON))
	typedDecoder.UseNumber()
	_ = typedDecoder.Decode(&typedRaw)
	if !reflect.DeepEqual(normalizeTree(raw), normalizeTree(typedRaw)) {
		t.Fatal("typed Config does not mirror schema defaults")
	}
}

func TestLoadSeedsAndAppliesPrecedence(t *testing.T) {
	root := t.TempDir()
	var warnings []string
	loaded, err := Load(Options{
		DataDir: root,
		Env:     []string{"VM_AGENT_MAX_STEPS=71"},
		Warn:    func(message string) { warnings = append(warnings, message) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Agent.MaxSteps != 71 {
		t.Fatalf("max steps = %d", loaded.Config.Agent.MaxSteps)
	}
	info, err := os.Stat(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if len(warnings) != 1 || strings.Contains(warnings[0], "71") {
		t.Fatalf("unsafe warnings: %#v", warnings)
	}
}

func TestStrictYAMLAndSecretRedaction(t *testing.T) {
	for _, input := range []string{
		"version:\t1\n",
		"version: 1\nversion: 1\n",
		"version: &v 1\n",
		"version: [1]\n",
	} {
		if _, _, err := ParseYAML([]byte(input)); err == nil {
			t.Fatalf("accepted forbidden YAML %q", input)
		}
	}
	const sentinel = "DO_NOT_LEAK_031"
	err := RedactError("secret ${env:SMTP_PASS}", sentinel)
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestDocsExportDeterministic(t *testing.T) {
	first, err := DocsJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := DocsJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || !strings.Contains(string(first), `"consoleDeepLink": "/config#llama-context-tokens"`) {
		t.Fatal("docs export is unstable or incomplete")
	}
}

func TestSchemaMetaValidationMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported keyword", func(root map[string]any) { root["bogus"] = true }},
		{"open object", func(root map[string]any) { root["additionalProperties"] = true }},
		{"missing default", func(root map[string]any) {
			schemaPath(root, "agent.maxSteps")["default"] = nil
			delete(schemaPath(root, "agent.maxSteps"), "default")
		}},
		{"bad pattern", func(root map[string]any) { schemaPath(root, "desktop.display")["pattern"] = "[" }},
		{"duplicate env", func(root map[string]any) { schemaPath(root, "agent.scrotPath")["x-vm-env"] = "VM_AGENT_MAX_STEPS" }},
		{"malformed docs", func(root map[string]any) {
			schemaPath(root, "agent.maxSteps")["x-vm-doc"].(map[string]any)["extra"] = true
		}},
		{"malformed ui", func(root map[string]any) {
			schemaPath(root, "agent.maxSteps")["x-vm-ui"].(map[string]any)["control"] = "checkbox"
		}},
		{"malformed secret", func(root map[string]any) { schemaPath(root, "mail.smarthost.password")["x-vm-sensitive"] = "path" }},
		{"duplicate order", func(root map[string]any) {
			schemaPath(root, "agent.keepTasks")["x-vm-ui"].(map[string]any)["order"] = json.Number("10")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := decodedSchema(t)
			test.mutate(root)
			if err := (&Schema{root: root}).metaValidate(); err == nil {
				t.Fatal("mutated schema unexpectedly passed")
			}
		})
	}
}

func TestValidatorSupportedKeywords(t *testing.T) {
	s := &Schema{root: map[string]any{}}
	tests := []struct {
		name    string
		node    map[string]any
		value   any
		wantBad bool
	}{
		{"null", map[string]any{"type": "null"}, nil, false},
		{"boolean", map[string]any{"type": "boolean"}, true, false},
		{"number", map[string]any{"type": "number", "minimum": json.Number("1"), "maximum": json.Number("2")}, 1.5, false},
		{"nan", map[string]any{"type": "number"}, math.NaN(), true},
		{"integer overflow", map[string]any{"type": "integer"}, json.Number("9223372036854775808"), true},
		{"string constraints", map[string]any{"type": "string", "minLength": json.Number("2"), "maxLength": json.Number("3"), "pattern": "^a"}, "abc", false},
		{"enum", map[string]any{"type": "string", "enum": []any{"a", "b"}}, "c", true},
		{"const", map[string]any{"type": "integer", "const": json.Number("2")}, int64(2), false},
		{"unique array", map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true}, []any{"a", "a"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bad := len(s.validateNode(test.node, test.value, "test", nil)) > 0
			if bad != test.wantBad {
				t.Fatalf("bad=%v, want %v", bad, test.wantBad)
			}
		})
	}
}

func TestYAMLValidAndInvalidCorpus(t *testing.T) {
	valid := []string{
		"\ufeffroot:\r\n  quoted: \"a\\tb\"\r\n  single: 'it''s'\r\n  enabled: true\r\n  count: -2\r\n  ratio: 1.5\r\n  nothing: null\r\n  emptyMap: {}\r\n  emptyList: []\r\n",
		"root:\n  rows:\n    - one\n    -\n      nested: two\n",
	}
	for _, input := range valid {
		if _, _, err := ParseYAML([]byte(input)); err != nil {
			t.Errorf("valid YAML rejected: %v", err)
		}
	}
	invalid := [][]byte{
		[]byte("---\nroot: 1\n"), []byte("%YAML 1.2\nroot: 1\n"), []byte("root: !tag value\n"),
		[]byte("root: &x value\n"), []byte("root: *x\n"), []byte("root: |\n  value\n"),
		[]byte("root: {a: 1}\n"), []byte("root:\n   child: 1\n"), []byte("root: \"\\q\"\n"),
		{0xff, 0xfe}, bytes.Repeat([]byte("a"), maxConfigBytes+1),
	}
	for _, input := range invalid {
		if _, _, err := ParseYAML(input); err == nil {
			t.Fatalf("invalid YAML accepted: %q", input[:min(len(input), 80)])
		}
	}
}

func TestInterpolationPrecedenceAndPortAdapters(t *testing.T) {
	root := t.TempDir()
	loaded, err := Load(Options{DataDir: root, Env: []string{
		"VM_AGENT_MAX_STEPS=71", "COUNT=72", "VM_LLAMA_PORT=18081",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Agent.MaxSteps != 71 ||
		loaded.Config.Llama.Address != "127.0.0.1:18081" ||
		loaded.Config.Llama.ChatCompletionsURL != "http://127.0.0.1:18081/v1/chat/completions" ||
		loaded.Config.Health.LlamaURL != "http://127.0.0.1:18081/health" {
		t.Fatalf("precedence/adapter mismatch: %#v", loaded.Config)
	}
	raw := deepCopy(loaded.Raw).(map[string]any)
	raw["agent"].(map[string]any)["maxSteps"] = "${env:COUNT}"
	validated, _, err := ValidateRaw(raw, root, []string{"COUNT=72"}, nil)
	if err != nil || validated.Config.Agent.MaxSteps != 72 ||
		validated.Raw["agent"].(map[string]any)["maxSteps"] != "${env:COUNT}" {
		t.Fatalf("interpolation mismatch: loaded=%#v err=%v", validated, err)
	}
	for _, env := range [][]string{{"VM_AGENT_MAX_STEPS="}, {"VM_AGENT_MAX_STEPS=wat"}} {
		if _, err := Load(Options{DataDir: t.TempDir(), Env: env}); err == nil {
			t.Fatalf("invalid override accepted: %v", env)
		}
	}
}

func TestSemanticValidationTables(t *testing.T) {
	schema, _ := EmbeddedSchema()
	defaults, _ := schema.Defaults()
	tests := []struct {
		path  string
		value any
	}{
		{"desktop.resolution", "200x900x24"},
		{"desktop.vncAddress", "0.0.0.0:5900"},
		{"desktop.cdpURL", "http://127.0.0.1:6379"},
		{"llama.chatCompletionsURL", "http://127.0.0.1:8081/wrong"},
		{"agent.bashPath", "relative/bash"},
		{"system.timezone", "Not/A_Zone"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			raw := deepCopy(defaults).(map[string]any)
			setPath(raw, test.path, test.value)
			if err := ValidateSemantic(raw); err == nil {
				t.Fatal("semantic violation accepted")
			}
		})
	}
}

func TestSecretResolverCacheRefreshAndRedaction(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "secret")
	if err := os.WriteFile(file, []byte("first\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver([]string{"SECRET=env-value"})
	now := time.Unix(100, 0)
	resolver.now = func() time.Time { return now }
	ref := "${file:" + file + "}"
	value, _, err := resolver.Resolve(ref, time.Minute)
	if err != nil || string(value) != "first" {
		t.Fatalf("initial resolve: %q %v", value, err)
	}
	_ = os.WriteFile(file, []byte("second\n"), 0o600)
	value, _, _ = resolver.Resolve(ref, time.Minute)
	if string(value) != "first" {
		t.Fatal("cache miss before TTL")
	}
	now = now.Add(2 * time.Minute)
	value, _, _ = resolver.Resolve(ref, time.Minute)
	if string(value) != "second" {
		t.Fatal("cache did not expire")
	}
	var revisions []SecretRevision
	unsubscribe := resolver.Subscribe(ref, func(revision SecretRevision) { revisions = append(revisions, revision) })
	_ = os.WriteFile(file, []byte("third\n"), 0o600)
	if _, err := resolver.Refresh(ref, time.Minute); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	if len(revisions) != 1 || revisions[0].Revision != 1 || !revisions[0].Success {
		t.Fatalf("bad revisions: %#v", revisions)
	}
	if strings.Contains(RedactError("failed DO_NOT_LEAK_031", "DO_NOT_LEAK_031").Error(), "DO_NOT_LEAK_031") {
		t.Fatal("redaction failed")
	}
}

func TestSecretResolverConcurrentMissCoalesces(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "secret")
	if err := os.WriteFile(file, []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(nil)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, _, err := resolver.Resolve("${file:"+file+"}", time.Minute)
			if err != nil || string(value) != "value" {
				t.Errorf("resolve=%q err=%v", value, err)
			}
		}()
	}
	wg.Wait()
}

func TestConditionalSecretResolutionAndArrayReferenceRejection(t *testing.T) {
	secretMeta := map[string]any{
		"cacheTtlSeconds": json.Number("300"), "allowEnv": true, "allowFile": true,
		"resolveWhen": map[string]any{"path": "integration.enabled", "equals": true},
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"integration": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"enabled":  map[string]any{"type": "boolean"},
					"password": map[string]any{"type": "string", "x-vm-secret": secretMeta},
					"rows": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	effective := map[string]any{"integration": map[string]any{
		"enabled": false, "password": "${env:MISSING}", "rows": []any{"literal"},
	}}
	raw := deepCopy(effective).(map[string]any)
	statuses := map[string]SecretStatus{}
	resolver := NewResolver(nil)
	if err := resolveTree(schema, effective, raw, "", t.TempDir(), map[string]string{}, resolver, statuses, effective); err != nil {
		t.Fatal(err)
	}
	if statuses["integration.password"].Status != "inactive" ||
		effective["integration"].(map[string]any)["password"] != "" {
		t.Fatalf("conditional secret was not inactive: %#v", statuses)
	}
	effective = map[string]any{"integration": map[string]any{
		"enabled": true, "password": "${env:MISSING}", "rows": []any{"${env:BAD}"},
	}}
	raw = deepCopy(effective).(map[string]any)
	if err := resolveTree(schema, effective, raw, "", t.TempDir(), map[string]string{}, resolver, map[string]SecretStatus{}, effective); err == nil {
		t.Fatal("active missing secret was accepted")
	}
	effective = map[string]any{"integration": map[string]any{
		"enabled": false, "password": "", "rows": []any{"${env:BAD}"},
	}}
	raw = deepCopy(effective).(map[string]any)
	if err := resolveTree(schema, effective, raw, "", t.TempDir(), map[string]string{}, resolver, map[string]SecretStatus{}, effective); err == nil {
		t.Fatal("array interpolation was accepted")
	}
}

func TestAtomicWriteFaultsAndDestinationSafety(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"create", "write", "sync", "close", "chmod", "rename"} {
		t.Run(step, func(t *testing.T) {
			atomicFault.Lock()
			atomicFault.hook = func(actual string) error {
				if actual == step {
					return errors.New("injected " + step)
				}
				return nil
			}
			atomicFault.Unlock()
			defer func() {
				atomicFault.Lock()
				atomicFault.hook = nil
				atomicFault.Unlock()
			}()
			if err := AtomicWrite(file, []byte("new")); err == nil {
				t.Fatal("fault did not fail")
			}
			content, _ := os.ReadFile(file)
			if string(content) != "old" {
				t.Fatalf("old file changed at %s", step)
			}
			entries, _ := os.ReadDir(root)
			for _, entry := range entries {
				if strings.Contains(entry.Name(), ".tmp-") {
					t.Fatalf("temporary artifact remains: %s", entry.Name())
				}
			}
		})
	}
	if err := AtomicWrite(file, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(link, []byte("bad")); err == nil {
		t.Fatal("destination symlink accepted")
	}
}

func TestInvalidPresentConfigIsNotRewritten(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, FileName)
	bad := []byte("version: 1\nunknown: true\n")
	if err := os.WriteFile(file, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Options{DataDir: root, Env: []string{}}); err == nil {
		t.Fatal("invalid present config loaded")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(after, bad) {
		t.Fatal("invalid present config was rewritten")
	}
}

func decodedSchema(t *testing.T) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(embeddedSchema))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	return root
}

func schemaPath(root map[string]any, dotted string) map[string]any {
	node := root
	for _, part := range strings.Split(dotted, ".") {
		node = node["properties"].(map[string]any)[part].(map[string]any)
	}
	return node
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
