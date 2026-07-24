package agent

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeCDPServer serves the /json target list and a websocket endpoint whose
// Runtime responses come from the respond callback. Frames may be fragmented.
func fakeCDPServer(t *testing.T, fragment bool, respond func(method string, params json.RawMessage) any) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			wsURL := "ws://" + server.Listener.Addr().String() + "/devtools/page/1"
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"type": "page", "url": "http://fake/", "webSocketDebuggerUrl": wsURL},
			})
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		key := r.Header.Get("Sec-WebSocket-Key")
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
			base64.StdEncoding.EncodeToString(sum[:]))
		for {
			payload, ok := readMaskedClientFrame(buffered.Reader)
			if !ok {
				return
			}
			var request struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(payload, &request) != nil {
				return
			}
			body, _ := json.Marshal(map[string]any{
				"id": request.ID, "result": respond(request.Method, request.Params),
			})
			if fragment && len(body) > 2 {
				half := len(body) / 2
				writeServerFrame(conn, 0x1, false, body[:half])
				writeServerFrame(conn, 0x0, true, body[half:])
			} else {
				writeServerFrame(conn, 0x1, true, body)
			}
		}
	}))
	return server
}

func readMaskedClientFrame(reader interface{ ReadByte() (byte, error) }) ([]byte, bool) {
	read := func(n int) ([]byte, bool) {
		data := make([]byte, n)
		for index := range data {
			value, err := reader.ReadByte()
			if err != nil {
				return nil, false
			}
			data[index] = value
		}
		return data, true
	}
	header, ok := read(2)
	if !ok {
		return nil, false
	}
	length := int(header[1] & 0x7f)
	switch length {
	case 126:
		extended, ok := read(2)
		if !ok {
			return nil, false
		}
		length = int(binary.BigEndian.Uint16(extended))
	case 127:
		extended, ok := read(8)
		if !ok {
			return nil, false
		}
		length = int(binary.BigEndian.Uint64(extended))
	}
	mask, ok := read(4)
	if !ok {
		return nil, false
	}
	payload, ok := read(length)
	if !ok {
		return nil, false
	}
	for index := range payload {
		payload[index] ^= mask[index%4]
	}
	return payload, header[0]&0x0f == 0x1
}

func writeServerFrame(conn interface{ Write([]byte) (int, error) }, opcode byte, fin bool, payload []byte) {
	first := opcode
	if fin {
		first |= 0x80
	}
	header := []byte{first}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127)
		var extended [8]byte
		binary.BigEndian.PutUint64(extended[:], uint64(len(payload)))
		header = append(header, extended[:]...)
	}
	_, _ = conn.Write(header)
	_, _ = conn.Write(payload)
}

func evaluateResult(value any) map[string]any {
	return map[string]any{"result": map[string]any{"value": value}}
}

func TestCDPReassemblesFragmentedFrames(t *testing.T) {
	server := fakeCDPServer(t, true, func(method string, _ json.RawMessage) any {
		if method != "Runtime.evaluate" {
			t.Errorf("unexpected method %s", method)
		}
		return evaluateResult(map[string]any{
			"url": "http://fake/page", "title": "Fake", "text": strings.Repeat("body text ", 50),
		})
	})
	defer server.Close()
	cdp := NewCDP(server.URL, server.Client())
	text, err := cdp.ReadPage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "http://fake/page") || !strings.Contains(text, "body text") {
		t.Fatalf("ReadPage = %q", text)
	}
}

func denseFixtureSnapshot() snapshotResult {
	// html > body > (tr wrapper with no text) + (a with href+text) + (span with text)
	snapshot := snapshotResult{
		Strings: []string{
			"HTML", "BODY", "TR", "A", "SPAN", // 0-4 tags
			"href", "item?id=1", "Story title", "block", "visible", "12 points", // 5-10
		},
		Documents: []snapshotDocument{{}},
	}
	document := &snapshot.Documents[0]
	document.Nodes.NodeName = []int{0, 1, 2, 3, 0, 4, 0}
	document.Nodes.NodeType = []int{1, 1, 1, 1, 3, 1, 3}
	document.Nodes.NodeValue = []int{0, 0, 0, 0, 7, 0, 10}
	document.Nodes.ParentIndex = []int{-1, 0, 1, 2, 3, 2, 5}
	document.Nodes.Attributes = [][]int{nil, nil, nil, {5, 6}, nil, nil, nil}
	document.Layout.NodeIndex = []int{0, 1, 2, 3, 5}
	document.Layout.Bounds = [][]float64{
		{0, 0, 1600, 900}, {0, 0, 1600, 900}, {0, 10, 1600, 20}, {5, 12, 300, 16}, {310, 12, 80, 16},
	}
	document.Layout.Styles = [][]int{{8, 9}, {8, 9}, {8, 9}, {8, 9}, {8, 9}}
	return snapshot
}

func TestDOMResultIsDenseWithURLTitleAndHintNote(t *testing.T) {
	snapshot := denseFixtureSnapshot()
	server := fakeCDPServer(t, false, func(method string, _ json.RawMessage) any {
		if method == "DOMSnapshot.captureSnapshot" {
			return snapshot
		}
		return evaluateResult(map[string]any{
			"url": "http://fake/list", "title": "Fake List", "ready": true,
			"offsetX": 0, "offsetY": 0, "scrollX": 0, "scrollY": 0,
		})
	})
	defer server.Close()
	cdp := NewCDP(server.URL, server.Client())

	text, boxes, err := cdp.DOM(context.Background(), "", 0, domCap)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		URL      string       `json:"url"`
		Title    string       `json:"title"`
		Note     string       `json:"note"`
		Elements []domElement `json:"elements"`
	}
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if result.URL != "http://fake/list" || result.Title != "Fake List" {
		t.Fatalf("url/title = %q %q", result.URL, result.Title)
	}
	tags := make([]string, 0)
	for _, element := range result.Elements {
		tags = append(tags, element.Tag)
	}
	// html/body/tr carry no text or attributes and are not interactive: dropped.
	if strings.Join(tags, ",") != "a,span" {
		t.Fatalf("dense tags = %v", tags)
	}
	if strings.Contains(text, `"box"`) {
		t.Fatal("serialized DOM must not contain box values")
	}
	if len(boxes) < 5 {
		t.Fatalf("server-side boxes must keep all rendered refs, got %d", len(boxes))
	}
	if result.Note != "" {
		t.Fatalf("unexpected note %q", result.Note)
	}

	// A CSS-selector-style hint matches nothing: unfiltered fallback plus note.
	text, _, err = cdp.DOM(context.Background(), `tr[id*="x"] td a`, 0, domCap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, hintMissNote) {
		t.Fatalf("hint miss note missing: %s", text)
	}
	if !strings.Contains(text, "Story title") {
		t.Fatalf("hint miss must fall back to unfiltered elements: %s", text)
	}

	// A substring hint filters as documented.
	text, _, err = cdp.DOM(context.Background(), "12 points", 0, domCap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "12 points") || strings.Contains(text, hintMissNote) {
		t.Fatalf("substring hint failed: %s", text)
	}
}

func TestNavigateWaitsForSettledPage(t *testing.T) {
	polls := 0
	server := fakeCDPServer(t, false, func(method string, _ json.RawMessage) any {
		if method != "Runtime.evaluate" {
			t.Errorf("unexpected method %s", method)
		}
		polls++
		switch {
		case polls == 1: // before-navigation state
			return evaluateResult(map[string]any{"url": "http://fake/old", "title": "Old", "ready": true})
		case polls == 2: // still on the old page
			return evaluateResult(map[string]any{"url": "http://fake/old", "title": "Old", "ready": true})
		case polls == 3: // loading
			return evaluateResult(map[string]any{"url": "http://fake/new", "title": "", "ready": false})
		default: // settled
			return evaluateResult(map[string]any{"url": "http://fake/new", "title": "New Page", "ready": true})
		}
	})
	defer server.Close()
	runner := &recordingRunner{}
	tools := NewLocalTools(Config{
		Runner: runner, XdotoolPath: "xdotool", CDPURL: server.URL, Client: server.Client(),
	}).(*localTools)
	started := time.Now()
	result, err := tools.Execute(context.Background(), "navigate", json.RawMessage(`{"url":"http://fake/new"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Observe {
		t.Fatal("navigate must produce an observation")
	}
	for _, want := range []string{`"url":"http://fake/new"`, `"title":"New Page"`, `"ready":true`} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("navigate observation %q missing %s", result.Text, want)
		}
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("navigate settle took %s", elapsed)
	}
}
