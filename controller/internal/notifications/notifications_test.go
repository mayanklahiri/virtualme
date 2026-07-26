package notifications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRegistryAndValidation(t *testing.T) {
	registry := Registry()
	if len(registry) != 4 {
		t.Fatalf("registry length = %d", len(registry))
	}
	want := []string{"info", "success", "warning", "error"}
	wantIcons := []string{"i-circle-info", "i-circle-check", "i-triangle-alert", "i-circle-x"}
	for i, name := range want {
		if registry[i].Name != name || registry[i].Icon != wantIcons[i] || registry[i].DefaultRenderer != "generic" {
			t.Fatalf("registry[%d] = %#v", i, registry[i])
		}
	}
	registry[0].AllowedRenderers[0] = "changed"
	registry[0].Name = "changed"
	if Registry()[0].Name != "info" || Registry()[0].AllowedRenderers[0] != "generic" {
		t.Fatal("Registry did not return a defensive copy")
	}

	for _, request := range []CreateRequest{
		{Type: "missing", Sender: "agent", Title: "x", Summary: "x", Renderer: "generic"},
		{Type: "info", Sender: "Bad", Title: "x", Summary: "x", Renderer: "generic"},
		{Type: "info", Sender: "agent", Subtype: "Bad", Title: "x", Summary: "x", Renderer: "generic"},
		{Type: "info", Sender: "agent", Title: "x", Summary: "x", Renderer: "missing"},
		{Type: "warning", Sender: "agent", Title: "x", Summary: "x", Renderer: "configuration"},
	} {
		if _, err := validateCreate(request, time.UnixMilli(1)); err == nil {
			t.Fatalf("accepted %#v", request)
		}
	}
}

func TestIdentifierValidationTables(t *testing.T) {
	base := CreateRequest{Type: "info", Sender: "agent", Renderer: "generic", Title: "x", Summary: "x"}
	for _, sender := range []string{"a", "telegram", "a.b-c_d", "a" + strings.Repeat("0", 31)} {
		request := base
		request.Sender = sender
		if _, err := validateCreate(request, testClock()); err != nil {
			t.Errorf("sender %q: %v", sender, err)
		}
	}
	for _, sender := range []string{"", "A", "0a", "a/", "a" + strings.Repeat("0", 32)} {
		request := base
		request.Sender = sender
		if _, err := validateCreate(request, testClock()); err == nil {
			t.Errorf("accepted sender %q", sender)
		}
	}
	for _, subtype := range []string{"x", "background.result", "a" + strings.Repeat("0", 47)} {
		request := base
		request.Subtype = subtype
		if _, err := validateCreate(request, testClock()); err != nil {
			t.Errorf("subtype %q: %v", subtype, err)
		}
	}
	for _, subtype := range []string{"A", "0x", "x/", "a" + strings.Repeat("0", 48)} {
		request := base
		request.Subtype = subtype
		if _, err := validateCreate(request, testClock()); err == nil {
			t.Errorf("accepted subtype %q", subtype)
		}
	}
}

func TestTextAndDetailSanitization(t *testing.T) {
	request := CreateRequest{
		Type: "info", Sender: "agent", Renderer: "agent",
		Title: " \xff A\t\u202eB\n ", Summary: " x\r\n y ",
		Detail: json.RawMessage(`{"z":" a\u0001\t b ","nested":{"x":1}}`),
	}
	got, err := validateCreate(request, time.UnixMilli(100))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "� A B" || got.Summary != "x y" {
		t.Fatalf("sanitized text = %q / %q", got.Title, got.Summary)
	}
	if string(got.Detail.Data) != `{"nested":{"x":1},"z":"a b"}` {
		t.Fatalf("detail = %s", got.Detail.Data)
	}

	for _, input := range []string{
		`[]`, `{} trailing`, `{"html":"x"}`, `{"x":{"STYLE":"x"}}`,
		`{"a":1,"\u0061":2}`, `{"x":"` + strings.Repeat("x", 2049) + `"}`,
	} {
		request.Detail = json.RawMessage(input)
		if _, err := validateCreate(request, time.UnixMilli(100)); err == nil {
			t.Fatalf("accepted detail %q", input)
		}
	}
	request.Detail = bytes.Repeat([]byte("x"), 8193)
	if _, err := validateCreate(request, time.UnixMilli(100)); err == nil {
		t.Fatal("accepted oversized detail")
	}
}

func TestDetailStructuralLimitsAndForbiddenKeys(t *testing.T) {
	base := CreateRequest{
		Type: "info", Sender: "agent", Renderer: "agent", Title: "x", Summary: "x",
	}
	depth := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":1}}}}}}}}`
	base.Detail = json.RawMessage(depth)
	if _, err := validateCreate(base, testClock()); err == nil {
		t.Fatal("accepted detail beyond nesting depth")
	}
	var nodes strings.Builder
	nodes.WriteString(`{"items":[`)
	for index := 0; index < 257; index++ {
		if index > 0 {
			nodes.WriteByte(',')
		}
		nodes.WriteString("0")
	}
	nodes.WriteString(`]}`)
	base.Detail = json.RawMessage(nodes.String())
	if _, err := validateCreate(base, testClock()); err == nil {
		t.Fatal("accepted more than 256 nodes")
	}
	for _, key := range []string{"html", "SVG", "innerHTML", "script", "style", "renderer", "component"} {
		base.Detail = json.RawMessage(fmt.Sprintf(`{"outer":{"%s":"x"}}`, key))
		if _, err := validateCreate(base, testClock()); err == nil {
			t.Errorf("accepted forbidden key %q", key)
		}
	}
	base.Detail = json.RawMessage(`{"\u202ea":1,"a":2}`)
	if _, err := validateCreate(base, testClock()); err == nil {
		t.Fatal("accepted duplicate keys after bidi sanitization")
	}
	base.Detail = json.RawMessage(`{"":1}`)
	if _, err := validateCreate(base, testClock()); err == nil {
		t.Fatal("accepted empty key")
	}
}

func TestTextBoundaries(t *testing.T) {
	base := CreateRequest{Type: "info", Sender: "agent", Renderer: "generic", Title: "x", Summary: "x"}
	for _, test := range []struct {
		field string
		n     int
		ok    bool
	}{{"title", 0, false}, {"title", 1, true}, {"title", 120, true}, {"title", 121, false},
		{"summary", 0, false}, {"summary", 1, true}, {"summary", 240, true}, {"summary", 241, false}} {
		request := base
		if test.field == "title" {
			request.Title = strings.Repeat("界", test.n)
		} else {
			request.Summary = strings.Repeat("界", test.n)
		}
		_, err := validateCreate(request, time.UnixMilli(100))
		if (err == nil) != test.ok {
			t.Fatalf("%s %d: err=%v", test.field, test.n, err)
		}
	}
}

func TestULIDMonotonicAndAlphabet(t *testing.T) {
	times := []time.Time{time.UnixMilli(10), time.UnixMilli(10), time.UnixMilli(9), time.UnixMilli(11)}
	index := 0
	g := newULIDGenerator(func() time.Time {
		value := times[index]
		index++
		return value
	}, bytes.NewReader(make([]byte, 80)))
	var previous string
	for range times {
		id, err := g.next()
		if err != nil {
			t.Fatal(err)
		}
		if !validULID(id) || len(id) != 26 {
			t.Fatalf("invalid ULID %q", id)
		}
		if previous != "" && id <= previous {
			t.Fatalf("not monotonic: %q <= %q", id, previous)
		}
		previous = id
	}
}

func TestULIDEntropyOverflowWaitsForClockAdvance(t *testing.T) {
	times := []time.Time{
		time.UnixMilli(10),
		time.UnixMilli(10),
		time.UnixMilli(11),
	}
	index := 0
	clock := func() time.Time {
		value := times[min(index, len(times)-1)]
		index++
		return value
	}
	entropy := append(bytes.Repeat([]byte{0xff}, 10), bytes.Repeat([]byte{0}, 10)...)
	generator := newULIDGenerator(clock, bytes.NewReader(entropy))
	first, err := generator.next()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.next()
	if err != nil {
		t.Fatal(err)
	}
	if second <= first || generator.lastMS != 11 {
		t.Fatalf("overflow IDs %s then %s at %d", first, second, generator.lastMS)
	}
}
