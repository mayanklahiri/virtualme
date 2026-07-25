package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mayanklahiri/virtualme/controller/internal/actuation"
	"github.com/mayanklahiri/virtualme/controller/internal/jobs"
	"github.com/mayanklahiri/virtualme/controller/internal/tts"
)

const (
	outputCap        = 64 * 1024
	domCap           = 24 * 1024
	pageEvalCap      = 16 * 1024
	layoutDebugCap   = 4 * 1024
	navigateSettleMs = 15000
)

// Runner executes a process and returns its captured output.
type Runner interface {
	Run(context.Context, string, []string, []string, string) ([]byte, []byte, error)
}

type processRunner struct{}

// NewProcessRunner returns the controller's subprocess Runner implementation.
func NewProcessRunner() Runner {
	return processRunner{}
}

func (processRunner) Run(ctx context.Context, name string, args, env []string, dir string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = mergeEnvironment(os.Environ(), env)
	command.Dir = dir
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr cappedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return stdout.Bytes(), stderr.Bytes(), err
	case <-ctx.Done():
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		<-done
		return stdout.Bytes(), stderr.Bytes(), ctx.Err()
	}
}

func mergeEnvironment(base, overrides []string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	add := func(entry string) {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return
		}
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = entry
	}
	for _, entry := range base {
		add(entry)
	}
	for _, entry := range overrides {
		add(entry)
	}
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, values[key])
	}
	return result
}

type cappedBuffer struct {
	data []byte
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := outputCap - len(b.data)
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.data = append(b.data, data...)
	}
	return original, nil
}

func (b *cappedBuffer) Bytes() []byte { return b.data }

type localTools struct {
	cfg       Config
	runner    Runner
	cdp       *CDP
	width     int
	height    int
	apiWidth  int
	apiHeight int
	boxMu     sync.Mutex
	boxes     map[int][4]float64
	cwd       string
	env       map[string]string
	taskID    string
	stepID    string
}

func (t *localTools) resetTask(taskID string) {
	t.boxMu.Lock()
	t.boxes = make(map[int][4]float64)
	t.boxMu.Unlock()
	t.cwd = t.cfg.DataDir
	t.env = make(map[string]string)
	t.taskID = taskID
	t.stepID = ""
}

// NewLocalTools constructs all built-in observation and action tools.
func NewLocalTools(cfg Config) *localTools {
	runner := cfg.Runner
	if runner == nil {
		runner = processRunner{}
	}
	width, height := parseResolution(cfg.Resolution)
	apiWidth, apiHeight := apiDimensions(width, height)
	if cfg.Display == "" {
		cfg.Display = ":99"
	}
	if cfg.XdotoolPath == "" {
		cfg.XdotoolPath = "xdotool"
	}
	if cfg.ScrotPath == "" {
		cfg.ScrotPath = "scrot"
	}
	if cfg.ConvertPath == "" {
		cfg.ConvertPath = "convert"
	}
	if cfg.BashPath == "" {
		cfg.BashPath = "bash"
	}
	return &localTools{
		cfg: cfg, runner: runner, cdp: NewCDP(cfg.CDPURL, cfg.Client),
		width: width, height: height, apiWidth: apiWidth, apiHeight: apiHeight,
		boxes: make(map[int][4]float64), cwd: cfg.DataDir, env: make(map[string]string),
	}
}

func schema(value string) json.RawMessage { return json.RawMessage(value) }

// ToolManifest is the server-driven description consumed by the Tools page.
type ToolManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

func (t *localTools) Definitions() []Tool {
	return []Tool{
		{Name: "screenshot", Description: "Capture the visible browser screen. Agent observations include an API-coordinate grid; manual console calls return a pure capture.", Schema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "dom", Description: "Read rendered visible DOM elements with stable refs for click_element/type_into. selectorHint is a case-insensitive substring matched against tag/text/attributes - NOT a CSS selector. Large pages paginate; pass page to continue.", Schema: schema(`{"type":"object","properties":{"selectorHint":{"type":"string","description":"substring filter (not CSS)"},"page":{"type":"integer","minimum":0}},"additionalProperties":false}`)},
		{Name: "read_page", Description: "Read the current page as a structured YAML digest: title, url, head metadata, and a simplified hierarchy of important visible elements. Numbered feed articles expose rank, title, title_link (ready-to-copy Markdown linked to the comment page), url, score, comments, comment_url, author, and age. Other content includes headings, text blocks, links (href), images (src, alt), media, tables (rows), lists, and forms. This is the primary tool for extracting page information; prefer it over screenshots or dom, and use dom_query or dom for targeted follow-up.", Schema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "dom_query", Description: "Extract structured text and attributes from elements matching a precise CSS selector.", Schema: schema(`{"type":"object","properties":{"selector":{"type":"string","description":"CSS selector evaluated in the page"},"attributes":{"type":"array","items":{"type":"string"},"description":"attribute names to return; default: text only"},"limit":{"type":"integer","minimum":1,"maximum":50,"default":10}},"required":["selector"],"additionalProperties":false}`)},
		{Name: "dom_validate", Description: "Evaluate page structure and content assertions without short-circuiting.", Schema: schema(`{"type":"object","properties":{"assertions":{"type":"array","maxItems":10,"items":{"type":"object","properties":{"selector":{"type":"string"},"exists":{"type":"boolean"},"minCount":{"type":"integer"},"textContains":{"type":"string"},"attribute":{"type":"string"},"attributeEquals":{"type":"string"}},"required":["selector"],"additionalProperties":false}}},"required":["assertions"],"additionalProperties":false}`)},
		{Name: "page_eval", Description: "Evaluate one bounded read-only JavaScript expression and return its JSON value.", Schema: schema(`{"type":"object","properties":{"expression":{"type":"string","maxLength":2000,"description":"A single JavaScript expression evaluated read-only in the page; its JSON-stringified value is returned (max 16 KiB). Mutation attempts fail."}},"required":["expression"],"additionalProperties":false}`)},
		{Name: "layout_debug", Description: "Inspect geometry, computed visibility, occlusion, and scroll state for exactly one DOM ref or CSS selector.", Schema: schema(`{"type":"object","properties":{"ref":{"type":"string"},"selector":{"type":"string"}},"additionalProperties":false}`)},
		{Name: "click", Description: "Click API-space screenshot coordinates via OS input.", Schema: schema(`{"type":"object","properties":{"x":{"type":"number"},"y":{"type":"number"}},"required":["x","y"],"additionalProperties":false}`)},
		{Name: "click_element", Description: "Click the center of a DOM ref via OS input.", Schema: schema(`{"type":"object","properties":{"ref":{"type":"integer"}},"required":["ref"],"additionalProperties":false}`)},
		{Name: "type", Description: "Type text into the focused control via OS input.", Schema: schema(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`)},
		{Name: "type_into", Description: "Click a DOM ref and type text via OS input.", Schema: schema(`{"type":"object","properties":{"ref":{"type":"integer"},"text":{"type":"string"}},"required":["ref","text"],"additionalProperties":false}`)},
		{Name: "key", Description: "Press a key or chord such as ctrl+l or Return via OS input.", Schema: schema(`{"type":"object","properties":{"keys":{"type":"string"}},"required":["keys"],"additionalProperties":false}`)},
		{Name: "scroll", Description: "Scroll the visible page via OS mouse wheel input.", Schema: schema(`{"type":"object","properties":{"dir":{"type":"string","enum":["up","down"]},"amount":{"type":"integer","minimum":1,"maximum":50}},"required":["dir"],"additionalProperties":false}`)},
		{Name: "navigate", Description: "Navigate by focusing Chromium's omnibox and typing a URL via OS input; waits for the page to settle and returns the resulting URL, title, and readiness.", Schema: schema(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`)},
		{Name: "bash", Description: "Run a one-shot bash command in the container; cwd and exported variables persist for this task.", Schema: schema(`{"type":"object","properties":{"command":{"type":"string"},"timeoutSec":{"type":"integer","minimum":1,"maximum":300}},"required":["command"],"additionalProperties":false}`)},
		{Name: "system_info", Description: "Probe the local OS, packages, environment, paths, disk, and services.", Schema: schema(`{"type":"object","properties":{"topic":{"type":"string","enum":["os","packages","env","paths","all"]}},"additionalProperties":false}`)},
		{Name: "speak", Description: "Speak text aloud to the user through the console (local text-to-speech). Use when the user asks to hear something or an audible response is clearly better.", Schema: schema(`{"type":"object","properties":{"text":{"type":"string","maxLength":4096},"speed":{"type":"number","minimum":0.5,"maximum":2}},"required":["text"],"additionalProperties":false}`)},
	}
}

// Manifest directly re-serializes Definitions for the authoritative Tools page.
func (t *localTools) Manifest() []ToolManifest {
	definitions := t.Definitions()
	manual := Tool{
		Name:        "dump_dom",
		Description: "Capture the current page DOM as a development golden-fixture JSON file under the data volume.",
		Schema:      schema(`{"type":"object","properties":{},"additionalProperties":false}`),
	}
	manifest := make([]ToolManifest, 0, len(definitions)+1)
	definitions = append(definitions, manual)
	for _, definition := range definitions {
		manifest = append(manifest, ToolManifest{
			Name: definition.Name, Description: definition.Description, Schema: definition.Schema,
		})
	}
	return manifest
}

// Has reports whether name is present in the authoritative tool definitions.
func (t *localTools) Has(name string) bool {
	for _, definition := range t.Manifest() {
		if definition.Name == name {
			return true
		}
	}
	return false
}

func decodeArgs(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// ExecuteManual runs one tool on behalf of the Tools console. It behaves
// exactly like Execute except screenshots return a pure capture: the
// API-coordinate grid exists only to ground the agent's vision (X1).
func (t *localTools) ExecuteManual(ctx context.Context, name string, raw json.RawMessage) (ToolResult, error) {
	if name == "dump_dom" {
		if err := decodeArgs(raw, &struct{}{}); err != nil {
			return ToolResult{}, err
		}
		return t.dumpDOM(ctx)
	}
	if name == "screenshot" {
		image, err := t.screenshot(ctx, false)
		return ToolResult{Text: fmt.Sprintf("screenshot %dx%d API space; display %dx%d", t.apiWidth, t.apiHeight, t.width, t.height), ImageJPEG: image, Summary: "Captured screenshot", Observe: true}, err
	}
	return t.Execute(ctx, name, raw)
}

func (t *localTools) dumpDOM(ctx context.Context) (ToolResult, error) {
	dump, err := t.cdp.DumpDOM(ctx)
	if err != nil {
		return ToolResult{}, err
	}
	encoded, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return ToolResult{}, fmt.Errorf("encode DOM dump: %w", err)
	}
	rawURL, _ := dump["url"].(string)
	parsed, _ := url.Parse(rawURL)
	host := strings.ToLower(parsed.Hostname())
	host = regexp.MustCompile(`[^a-z0-9.-]+`).ReplaceAllString(host, "-")
	host = strings.Trim(host, ".-")
	if host == "" {
		host = "page"
	}
	dir := filepath.Join(t.cfg.DataDir, "dom-dumps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolResult{}, fmt.Errorf("create DOM dump directory: %w", err)
	}
	name := fmt.Sprintf("%s-%d.dom.json", host, time.Now().UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, name), append(encoded, '\n'), 0o644); err != nil {
		return ToolResult{}, fmt.Errorf("write DOM dump: %w", err)
	}
	relative := filepath.ToSlash(filepath.Join(filepath.Base(t.cfg.DataDir), "dom-dumps", name))
	return ToolResult{Text: relative, Summary: "Captured DOM fixture"}, nil
}

func (t *localTools) Execute(ctx context.Context, name string, raw json.RawMessage) (ToolResult, error) {
	if usesXdotool(name) {
		actuation.Lock()
		defer actuation.Unlock()
	}
	switch name {
	case "screenshot":
		image, err := t.screenshot(ctx, true)
		return ToolResult{Text: fmt.Sprintf("screenshot %dx%d API space; display %dx%d", t.apiWidth, t.apiHeight, t.width, t.height), ImageJPEG: image, Summary: "Captured screenshot", Observe: true}, err
	case "dom":
		var args struct {
			SelectorHint string `json:"selectorHint"`
			Page         int    `json:"page"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		text, boxes, err := t.cdp.DOM(ctx, args.SelectorHint, args.Page, domCap)
		if err == nil {
			t.boxMu.Lock()
			t.boxes = boxes
			t.boxMu.Unlock()
		}
		return ToolResult{Text: text, Summary: "Observed rendered DOM", Observe: true}, err
	case "read_page":
		text, err := t.cdp.ReadPage(ctx)
		return ToolResult{Text: text, Summary: "Read page digest", Observe: true}, err
	case "dom_query":
		return t.domQuery(ctx, raw)
	case "dom_validate":
		return t.domValidate(ctx, raw)
	case "page_eval":
		return t.pageEval(ctx, raw)
	case "layout_debug":
		return t.layoutDebug(ctx, raw)
	case "click":
		var args struct{ X, Y float64 }
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		x, y := apiToScreen(args.X, args.Y, t.width, t.height, t.apiWidth, t.apiHeight)
		return t.action(ctx, "Clicked coordinates", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1")
	case "click_element":
		var args struct {
			Ref int `json:"ref"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		return t.clickRef(ctx, args.Ref)
	case "type":
		var args struct {
			Text string `json:"text"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		return t.action(ctx, "Typed text", "type", "--clearmodifiers", "--delay", "1", "--", args.Text)
	case "type_into":
		var args struct {
			Ref  int    `json:"ref"`
			Text string `json:"text"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		if _, err := t.clickRef(ctx, args.Ref); err != nil {
			return ToolResult{}, err
		}
		return t.action(ctx, "Typed into element", "type", "--clearmodifiers", "--delay", "1", "--", args.Text)
	case "key":
		var args struct {
			Keys string `json:"keys"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		return t.action(ctx, "Pressed "+args.Keys, "key", "--clearmodifiers", args.Keys)
	case "scroll":
		var args struct {
			Dir    string `json:"dir"`
			Amount int    `json:"amount"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		if args.Dir != "up" && args.Dir != "down" {
			return ToolResult{}, errors.New("dir must be up or down")
		}
		if args.Amount <= 0 {
			args.Amount = 3
		}
		button := "5"
		if args.Dir == "up" {
			button = "4"
		}
		arguments := []string{}
		for range args.Amount {
			arguments = append(arguments, "click", button)
		}
		return t.action(ctx, "Scrolled "+args.Dir, arguments...)
	case "navigate":
		var args struct {
			URL string `json:"url"`
		}
		if err := decodeArgs(raw, &args); err != nil {
			return ToolResult{}, err
		}
		if strings.TrimSpace(args.URL) == "" {
			return ToolResult{}, errors.New("url is empty")
		}
		before := t.cdp.pageInfo(ctx)
		if _, err := t.action(ctx, "", "key", "--clearmodifiers", "ctrl+l"); err != nil {
			return ToolResult{}, err
		}
		if _, err := t.action(ctx, "", "type", "--clearmodifiers", "--delay", "1", "--", args.URL); err != nil {
			return ToolResult{}, err
		}
		if _, err := t.action(ctx, "", "key", "Return"); err != nil {
			return ToolResult{}, err
		}
		settled := t.cdp.WaitSettled(ctx, before.URL, navigateSettleMs*time.Millisecond)
		observation, _ := json.Marshal(map[string]any{
			"url": settled.URL, "title": settled.Title, "ready": settled.Ready,
		})
		return ToolResult{
			Text: string(observation), Summary: "Navigated via omnibox", Observe: true,
		}, nil
	case "bash":
		return t.bash(ctx, raw)
	case "system_info":
		return t.systemInfo(ctx, raw)
	case "speak":
		return t.speak(ctx, raw)
	default:
		return ToolResult{}, fmt.Errorf("unknown tool %q", name)
	}
}

func capToolText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const suffix = "\n…[truncated]"
	return text[:limit-len(suffix)] + suffix
}

// domQueryExpression builds the Runtime.evaluate expression for dom_query.
// Nil attributes must marshal as [] so the page never sees null.map(...).
func domQueryExpression(selector string, attributes []string, limit int) string {
	if attributes == nil {
		attributes = []string{}
	}
	quotedSelector, _ := json.Marshal(selector)
	quotedAttributes, _ := json.Marshal(attributes)
	return fmt.Sprintf(`JSON.stringify([...document.querySelectorAll(%s)].slice(0,%d).map(n=>({tag:n.tagName.toLowerCase(),text:(n.innerText||"").slice(0,200),attrs:Object.fromEntries(%s.map(a=>[a,n.getAttribute(a)]).filter(([,v])=>v!=null))})))`,
		quotedSelector, limit, quotedAttributes)
}

func (t *localTools) domQuery(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Selector   string   `json:"selector"`
		Attributes []string `json:"attributes"`
		Limit      int      `json:"limit"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	if strings.TrimSpace(args.Selector) == "" {
		return ToolResult{}, errors.New("selector is empty")
	}
	if args.Limit == 0 {
		args.Limit = 10
	}
	if args.Limit < 1 || args.Limit > 50 {
		return ToolResult{}, errors.New("limit must be between 1 and 50")
	}
	expression := domQueryExpression(args.Selector, args.Attributes, args.Limit)
	value, err := t.cdp.evaluate(ctx, expression, false)
	if err != nil {
		return ToolResult{}, err
	}
	text, ok := value.(string)
	if !ok {
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return ToolResult{}, marshalErr
		}
		text = string(encoded)
	}
	return ToolResult{Text: capToolText(text, domCap), Summary: "Queried rendered DOM"}, nil
}

func (t *localTools) domValidate(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Assertions []struct {
			Selector        string  `json:"selector"`
			Exists          *bool   `json:"exists"`
			MinCount        *int    `json:"minCount"`
			TextContains    *string `json:"textContains"`
			Attribute       *string `json:"attribute"`
			AttributeEquals *string `json:"attributeEquals"`
		} `json:"assertions"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	if len(args.Assertions) == 0 || len(args.Assertions) > 10 {
		return ToolResult{}, errors.New("assertions must contain 1-10 items")
	}
	for _, assertion := range args.Assertions {
		if strings.TrimSpace(assertion.Selector) == "" {
			return ToolResult{}, errors.New("assertion selector is empty")
		}
	}
	assertions, _ := json.Marshal(args.Assertions)
	expression := fmt.Sprintf(`(()=>{const assertions=%s;const results=assertions.map(a=>{const nodes=[...document.querySelectorAll(a.selector)];const checks=[];if("exists"in a)checks.push([(nodes.length>0)===a.exists,"exists"]);if("minCount"in a)checks.push([nodes.length>=a.minCount,"minCount"]);if("textContains"in a)checks.push([nodes.some(n=>(n.innerText||"").includes(a.textContains)),"textContains"]);if("attribute"in a){const values=nodes.map(n=>n.getAttribute(a.attribute));checks.push(["attributeEquals"in a?values.some(v=>v===a.attributeEquals):values.some(v=>v!==null),"attribute"]);}const pass=checks.every(([ok])=>ok);return{selector:a.selector,pass,count:nodes.length,detail:checks.length?(pass?"all assertions passed":checks.filter(([ok])=>!ok).map(([,name])=>name+" failed").join(", ")):"selector evaluated"};});return{pass:results.every(r=>r.pass),results};})()`, assertions)
	value, err := t.cdp.evaluate(ctx, expression, false)
	if err != nil {
		return ToolResult{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Text: capToolText(string(encoded), domCap), Summary: "Validated rendered DOM"}, nil
}

var pageEvalTripwires = []string{
	"document.write", "location=", "location.href=", "localstorage", "sessionstorage",
	"fetch(", "xmlhttprequest", "history.", "submit(", "click(",
}

func (t *localTools) pageEval(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Expression string `json:"expression"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	if strings.TrimSpace(args.Expression) == "" || len(args.Expression) > 2000 {
		return ToolResult{}, errors.New("expression must be 1-2000 characters")
	}
	normalized := strings.ToLower(args.Expression)
	for _, denied := range pageEvalTripwires {
		if strings.Contains(normalized, denied) {
			return ToolResult{}, fmt.Errorf("expression rejected by read-only policy: contains %q", denied)
		}
	}
	value, err := t.cdp.evaluate(ctx, "("+args.Expression+")", false, true)
	if err != nil {
		return ToolResult{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Text: capToolText(string(encoded), pageEvalCap), Summary: "Evaluated read-only page expression"}, nil
}

func (t *localTools) layoutDebug(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Ref      string `json:"ref"`
		Selector string `json:"selector"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	hasRef := strings.TrimSpace(args.Ref) != ""
	hasSelector := strings.TrimSpace(args.Selector) != ""
	if hasRef == hasSelector {
		return ToolResult{}, errors.New("exactly one of ref or selector is required")
	}
	var expression string
	if hasRef {
		ref, err := strconv.Atoi(args.Ref)
		if err != nil {
			return ToolResult{}, errors.New("ref must be an integer string")
		}
		t.boxMu.Lock()
		box, ok := t.boxes[ref]
		t.boxMu.Unlock()
		if !ok {
			return ToolResult{}, fmt.Errorf("unknown DOM ref %d; call dom again", ref)
		}
		boxJSON, _ := json.Marshal(box)
		expression = fmt.Sprintf(`(()=>{const serverBox=%s;const ox=screenX+(outerWidth-innerWidth)/2,oy=screenY+(outerHeight-innerHeight);const cx=serverBox[0]+serverBox[2]/2-ox,cy=serverBox[1]+serverBox[3]/2-oy;const n=document.elementFromPoint(cx,cy);return n?{ref:%d,serverBox,box:Object.fromEntries(["x","y","width","height","top","right","bottom","left"].map(k=>[k,n.getBoundingClientRect()[k]])),style:Object.fromEntries(["display","visibility","opacity","zIndex","pointerEvents"].map(k=>[k,getComputedStyle(n)[k]])),elementFromPoint:n.tagName.toLowerCase(),scroll:{x:scrollX,y:scrollY}}:{ref:%d,serverBox,error:"no element at ref center",scroll:{x:scrollX,y:scrollY}};})()`,
			boxJSON, ref, ref)
	} else {
		selector, _ := json.Marshal(args.Selector)
		expression = fmt.Sprintf(`(()=>{const n=document.querySelector(%s);if(!n)return{selector:%s,error:"selector matched nothing",scroll:{x:scrollX,y:scrollY}};const r=n.getBoundingClientRect(),cx=r.left+r.width/2,cy=r.top+r.height/2,hit=document.elementFromPoint(cx,cy);return{selector:%s,box:Object.fromEntries(["x","y","width","height","top","right","bottom","left"].map(k=>[k,r[k]])),style:Object.fromEntries(["display","visibility","opacity","zIndex","pointerEvents"].map(k=>[k,getComputedStyle(n)[k]])),elementFromPoint:hit?hit.tagName.toLowerCase():null,scroll:{x:scrollX,y:scrollY}};})()`,
			selector, selector, selector)
	}
	value, err := t.cdp.evaluate(ctx, expression, false)
	if err != nil {
		return ToolResult{}, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Text: capToolText(string(encoded), layoutDebugCap), Summary: "Inspected element layout"}, nil
}

func usesXdotool(name string) bool {
	switch name {
	case "click", "click_element", "type", "type_into", "key", "scroll", "navigate":
		return true
	default:
		return false
	}
}

func (t *localTools) speak(ctx context.Context, raw json.RawMessage) (result ToolResult, resultErr error) {
	started := time.Now()
	var args struct {
		Text  string  `json:"text"`
		Speed float64 `json:"speed"`
	}
	defer func() {
		if t.cfg.Activity != nil {
			voice := tts.DefaultVoice
			_ = t.cfg.Activity.Record(jobs.ActivityEvent{
				Kind: "tts", Name: "synthesize", JobID: jobs.JobID(ctx),
				Summary: fmt.Sprintf("Synthesized %d characters with %s", len([]rune(args.Text)), voice),
				Detail: jobs.ActivityDetail{
					DurationMS: time.Since(started).Milliseconds(), OK: resultErr == nil,
					Chars: len([]rune(args.Text)), Voice: voice,
				},
			})
		}
	}()
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	args.Text = strings.TrimSpace(args.Text)
	if args.Text == "" || len([]rune(args.Text)) > 4096 {
		return ToolResult{}, errors.New("text must be 1-4096 characters")
	}
	if args.Speed != 0 && (args.Speed < 0.5 || args.Speed > 2) {
		return ToolResult{}, errors.New("speed must be between 0.5 and 2.0")
	}
	if t.cfg.TTS == nil {
		return ToolResult{}, errors.New("local text-to-speech is unavailable")
	}
	origin, id := "chat", t.stepID
	if id == "" {
		id = t.taskID
	}
	queued, _ := json.Marshal(map[string]any{"type": "tts-status", "id": id, "origin": origin, "phase": "queued"})
	t.cfg.Broadcast(queued)
	sentenceCount := 0
	summary, err := t.cfg.TTS.Synthesize(ctx, tts.Request{
		Text: args.Text, Speed: args.Speed, Origin: "chat",
	}, func(event tts.Event) error {
		frame := map[string]any{"id": id, "origin": origin}
		switch event.Type {
		case "start":
			sentenceCount = event.Sentences
			frame["type"], frame["sampleRate"], frame["channels"], frame["sentences"] = "tts-start", event.SampleRate, event.Channels, event.Sentences
		case "chunk":
			frame["type"], frame["seq"], frame["pcm"] = "tts-chunk", event.Seq, event.PCM
			status, _ := json.Marshal(map[string]any{"type": "tts-status", "id": id, "origin": origin, "phase": "synthesizing", "sentence": event.Seq + 1, "sentences": sentenceCount})
			t.cfg.Broadcast(status)
		case "done":
			frame["type"], frame["audioSec"], frame["rtf"], frame["cached"] = "tts-done", event.AudioSec, event.RTF, event.Cached
		default:
			return nil
		}
		payload, _ := json.Marshal(frame)
		t.cfg.Broadcast(payload)
		return nil
	})
	if err != nil {
		if ctx.Err() != nil {
			stopped, _ := json.Marshal(map[string]any{"type": "tts-status", "id": id, "origin": origin, "phase": "stopped"})
			t.cfg.Broadcast(stopped)
			return ToolResult{}, ctx.Err()
		}
		payload, _ := json.Marshal(map[string]any{"type": "tts-error", "id": id, "origin": origin, "error": err.Error()})
		t.cfg.Broadcast(payload)
		failed, _ := json.Marshal(map[string]any{"type": "tts-status", "id": id, "origin": origin, "phase": "failed"})
		t.cfg.Broadcast(failed)
		return ToolResult{}, err
	}
	done, _ := json.Marshal(map[string]any{"type": "tts-status", "id": id, "origin": origin, "phase": "done", "sentences": sentenceCount, "rtf": summary.RTF})
	t.cfg.Broadcast(done)
	encoded, _ := json.Marshal(map[string]any{"ok": true, "audioSec": summary.AudioSec})
	return ToolResult{Text: string(encoded), Summary: "Spoke text aloud"}, nil
}

func (t *localTools) action(ctx context.Context, summary string, args ...string) (ToolResult, error) {
	stdout, stderr, err := t.runner.Run(ctx, t.cfg.XdotoolPath, args, []string{"DISPLAY=" + t.cfg.Display}, "")
	if err != nil {
		return ToolResult{}, fmt.Errorf("xdotool: %w: %s%s", err, stdout, stderr)
	}
	return ToolResult{Text: summary, Summary: summary}, nil
}

func (t *localTools) clickRef(ctx context.Context, ref int) (ToolResult, error) {
	t.boxMu.Lock()
	box, ok := t.boxes[ref]
	t.boxMu.Unlock()
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown DOM ref %d; call dom again", ref)
	}
	x := int(box[0] + box[2]/2)
	y := int(box[1] + box[3]/2)
	return t.action(ctx, fmt.Sprintf("Clicked element ref %d", ref), "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1")
}

func parseResolution(value string) (int, int) {
	width, height := 1600, 900
	parts := strings.Split(value, "x")
	if len(parts) >= 2 {
		if parsed, err := strconv.Atoi(parts[0]); err == nil && parsed > 0 {
			width = parsed
		}
		if parsed, err := strconv.Atoi(parts[1]); err == nil && parsed > 0 {
			height = parsed
		}
	}
	return width, height
}

func apiDimensions(width, height int) (int, int) {
	if width <= 1024 {
		return width, height
	}
	return 1024, int(float64(height)*1024/float64(width) + 0.5)
}

func apiToScreen(x, y float64, width, height, apiWidth, apiHeight int) (int, int) {
	screenX := int(x*float64(width)/float64(apiWidth) + 0.5)
	screenY := int(y*float64(height)/float64(apiHeight) + 0.5)
	screenX = max(0, min(width-1, screenX))
	screenY = max(0, min(height-1, screenY))
	return screenX, screenY
}

func (t *localTools) screenshot(ctx context.Context, withGrid bool) ([]byte, error) {
	tempDir, err := os.MkdirTemp("", "virtualme-capture-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	raw := filepath.Join(tempDir, "screen.png")
	final := filepath.Join(tempDir, "screen.jpg")
	if stdout, stderr, runErr := t.runner.Run(ctx, t.cfg.ScrotPath, []string{"-o", raw}, []string{"DISPLAY=" + t.cfg.Display}, ""); runErr != nil {
		return nil, fmt.Errorf("scrot: %w: %s%s", runErr, stdout, stderr)
	}
	args := []string{raw, "-resize", fmt.Sprintf("%dx%d!", t.apiWidth, t.apiHeight)}
	if withGrid {
		for _, item := range gridDraw(t.width, t.height) {
			args = append(args, "-draw", item)
		}
	}
	args = append(args, "-quality", "82", final)
	if stdout, stderr, runErr := t.runner.Run(ctx, t.cfg.ConvertPath, args, nil, ""); runErr != nil {
		return nil, fmt.Errorf("convert: %w: %s%s", runErr, stdout, stderr)
	}
	return os.ReadFile(final)
}

func gridDraw(screenWidth, screenHeight int) []string {
	apiWidth, apiHeight := apiDimensions(screenWidth, screenHeight)
	scaleX := float64(apiWidth) / float64(screenWidth)
	scaleY := float64(apiHeight) / float64(screenHeight)
	draw := []string{}
	for x := 100; x < screenWidth; x += 100 {
		ax := int(float64(x)*scaleX + 0.5)
		draw = append(draw,
			fmt.Sprintf("stroke rgba(255,255,255,0.45) fill none stroke-width 1 line %d,0 %d,%d", ax, ax, apiHeight),
			fmt.Sprintf("stroke black fill white text %d,14 '%d'", ax+2, ax),
		)
	}
	for y := 100; y < screenHeight; y += 100 {
		ay := int(float64(y)*scaleY + 0.5)
		draw = append(draw,
			fmt.Sprintf("stroke rgba(255,255,255,0.45) fill none stroke-width 1 line 0,%d %d,%d", ay, apiWidth, ay),
			fmt.Sprintf("stroke black fill white text 2,%d '%d'", ay-2, ay),
		)
	}
	return draw
}

var destructive = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[;&|[:space:]])rm[[:space:]]+-(?:[a-z]*r[a-z]*f|[a-z]*f[a-z]*r)[a-z]*[[:space:]]+(?:--[[:space:]]+)?/(?:\*)?(?:[[:space:]]|$)`),
	regexp.MustCompile(`(?i)(^|[;&|[:space:]])mkfs(?:\.|[[:space:]])`),
	regexp.MustCompile(`(?i)(^|[;&|[:space:]])dd[[:space:]].*\bof=/dev/`),
	regexp.MustCompile(`(?i)(>|tee[[:space:]]+|of=)/dev/(sd|nvme)`),
	regexp.MustCompile(`:\(\)[[:space:]]*\{[[:space:]]*:\|:&[[:space:]]*;?[[:space:]]*\}[[:space:]]*;[[:space:]]*:`),
}

func commandDenied(command string) bool {
	for _, pattern := range destructive {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func (t *localTools) bash(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeoutSec"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	if commandDenied(args.Command) {
		return ToolResult{}, errors.New("command refused by destructive-command safety policy")
	}
	if args.TimeoutSec <= 0 {
		args.TimeoutSec = 60
	}
	if args.TimeoutSec > 300 {
		return ToolResult{}, errors.New("timeoutSec exceeds 300")
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(args.TimeoutSec)*time.Second)
	defer cancel()
	env := make([]string, 0, len(t.env))
	for key, value := range t.env {
		env = append(env, key+"="+value)
	}
	wrapper := args.Command + "\nprintf '\\n__VM_CWD__%s\\n' \"$PWD\"\nprintf '__VM_ENV__\\n'\nenv -0"
	stdout, stderr, err := t.runner.Run(runCtx, t.cfg.BashPath, []string{"-lc", wrapper}, env, t.cwd)
	text := string(stdout)
	if marker := strings.LastIndex(text, "\n__VM_CWD__"); marker >= 0 {
		meta := text[marker+len("\n__VM_CWD__"):]
		text = text[:marker]
		if cut := strings.IndexByte(meta, '\n'); cut >= 0 {
			t.cwd = strings.TrimSpace(meta[:cut])
			envBlock := meta[cut+1:]
			if strings.HasPrefix(envBlock, "__VM_ENV__\n") {
				for _, entry := range strings.Split(strings.TrimPrefix(envBlock, "__VM_ENV__\n"), "\x00") {
					key, value, ok := strings.Cut(entry, "=")
					if ok && key != "" {
						t.env[key] = value
					}
				}
			}
		}
	}
	result := strings.TrimSpace(text)
	if len(stderr) > 0 {
		result += "\nstderr:\n" + string(stderr)
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return ToolResult{Text: result}, errors.New("command timed out")
	}
	if err != nil {
		return ToolResult{Text: result}, fmt.Errorf("command exited with error: %w", err)
	}
	return ToolResult{Text: result, Summary: "Ran bash command"}, nil
}

func (t *localTools) systemInfo(ctx context.Context, raw json.RawMessage) (ToolResult, error) {
	var args struct {
		Topic string `json:"topic"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return ToolResult{}, err
	}
	if args.Topic == "" {
		args.Topic = "all"
	}
	commands := map[string]string{
		"os":       `cat /etc/os-release; uname -a`,
		"packages": `chromium --version; bash --version | sed -n '1p'; xdotool version; scrot --version`,
		"env":      `env | grep -E '^(VM_|XDG_|DISPLAY=|HOME=|PATH=)' | sort`,
		"paths":    `printf 'data=%s\n' "$VM_DATA_DIR"; df -h "$VM_DATA_DIR"; pgrep -a 'chromium|llama|valkey|controller' || true`,
		"all":      `cat /etc/os-release; uname -a; chromium --version; env | grep -E '^(VM_|XDG_|DISPLAY=|HOME=|PATH=)' | sort; df -h "$VM_DATA_DIR"; pgrep -a 'chromium|llama|valkey|controller' || true`,
	}
	command, ok := commands[args.Topic]
	if !ok {
		return ToolResult{}, errors.New("invalid system_info topic")
	}
	stdout, stderr, err := t.runner.Run(ctx, t.cfg.BashPath, []string{"-lc", command}, nil, t.cwd)
	if err != nil {
		return ToolResult{}, fmt.Errorf("system probe: %w: %s", err, stderr)
	}
	return ToolResult{Text: string(stdout), Summary: "Read system information", Observe: true}, nil
}
