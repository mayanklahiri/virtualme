package notifications

import (
	"bytes"
	"encoding/json"
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
	for i, name := range want {
		if registry[i].Name != name || registry[i].Icon == "" || registry[i].DefaultRenderer != "generic" {
			t.Fatalf("registry[%d] = %#v", i, registry[i])
		}
	}
	registry[0].Name = "changed"
	if Registry()[0].Name != "info" {
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
