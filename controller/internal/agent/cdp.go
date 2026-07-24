package agent

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CDP is a deliberately read-only Chrome DevTools Protocol client.
type CDP struct {
	base   string
	client *http.Client
}

// NewCDP constructs a read-only client from the target-list HTTP endpoint.
func NewCDP(base string, client *http.Client) *CDP {
	if base == "" {
		base = "http://127.0.0.1:9222"
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &CDP{base: strings.TrimRight(base, "/"), client: client}
}

type cdpTarget struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func (c *CDP) target(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/json", nil)
	if err != nil {
		return "", err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("CDP target list returned %s", response.Status)
	}
	var targets []cdpTarget
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&targets); err != nil {
		return "", err
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", errors.New("CDP has no page target")
}

func (c *CDP) call(ctx context.Context, method string, params any, result any) error {
	endpoint, err := c.target(ctx)
	if err != nil {
		return err
	}
	conn, reader, err := dialWebSocket(ctx, endpoint)
	if err != nil {
		return err
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	payload, _ := json.Marshal(map[string]any{"id": 1, "method": method, "params": params})
	if err := writeClientFrame(conn, payload); err != nil {
		return err
	}
	var message []byte
	messageOpcode := byte(0)
	for {
		frame, opcode, fin, err := readServerFrame(reader)
		if err != nil {
			return err
		}
		switch opcode {
		case 0x9:
			if err := writeClientControl(conn, 0xA, frame); err != nil {
				return err
			}
			continue
		case 0x8:
			return errors.New("CDP websocket closed")
		case 0x1, 0x2:
			message = append(message[:0], frame...)
			messageOpcode = opcode
		case 0x0:
			message = append(message, frame...)
		default:
			continue
		}
		if len(message) > maxCDPMessage {
			return errors.New("CDP message exceeds 16 MiB")
		}
		if !fin || messageOpcode != 0x1 {
			continue
		}
		var envelope struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(message, &envelope) != nil || envelope.ID != 1 {
			continue
		}
		if envelope.Error != nil {
			return errors.New(envelope.Error.Message)
		}
		return json.Unmarshal(envelope.Result, result)
	}
}

func dialWebSocket(ctx context.Context, rawURL string) (net.Conn, *bufio.Reader, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	if parsed.Scheme != "ws" || parsed.Host == "" {
		return nil, nil, errors.New("CDP debugger URL must be ws://")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", parsed.Host)
	if err != nil {
		return nil, nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		conn.Close()
		return nil, nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	path := parsed.RequestURI()
	if path == "" {
		path = "/"
	}
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", path, parsed.Host, key); err != nil {
		conn.Close()
		return nil, nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, nil, fmt.Errorf("CDP websocket upgrade returned %s", response.Status)
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	want := base64.StdEncoding.EncodeToString(sum[:])
	if response.Header.Get("Sec-WebSocket-Accept") != want {
		conn.Close()
		return nil, nil, errors.New("invalid CDP websocket accept key")
	}
	return conn, reader, nil
}

func writeClientFrame(writer io.Writer, payload []byte) error {
	return writeMaskedFrame(writer, 0x1, payload)
}

func writeClientControl(writer io.Writer, opcode byte, payload []byte) error {
	if len(payload) > 125 {
		payload = payload[:125]
	}
	return writeMaskedFrame(writer, opcode, payload)
}

func writeMaskedFrame(writer io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 65535:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(length))
		header = append(header, encoded[:]...)
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%4]
	}
	if _, err := writer.Write(header); err != nil {
		return err
	}
	_, err := writer.Write(masked)
	return err
}

const maxCDPMessage = 16 * 1024 * 1024

func readServerFrame(reader *bufio.Reader) ([]byte, byte, bool, error) {
	var header [2]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, 0, false, err
	}
	if header[1]&0x80 != 0 {
		return nil, 0, false, errors.New("CDP server frame unexpectedly masked")
	}
	fin := header[0]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var value [2]byte
		if _, err := io.ReadFull(reader, value[:]); err != nil {
			return nil, 0, false, err
		}
		length = uint64(binary.BigEndian.Uint16(value[:]))
	case 127:
		var value [8]byte
		if _, err := io.ReadFull(reader, value[:]); err != nil {
			return nil, 0, false, err
		}
		length = binary.BigEndian.Uint64(value[:])
	}
	if length > maxCDPMessage {
		return nil, 0, false, errors.New("CDP frame exceeds 16 MiB")
	}
	payload := make([]byte, int(length))
	_, err := io.ReadFull(reader, payload)
	return payload, header[0] & 0x0f, fin, err
}

// ReadPage returns URL, title, and visible rendered text without changing state.
func (c *CDP) ReadPage(ctx context.Context) (string, error) {
	var result struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	expression := `({url:location.href,title:document.title,text:(document.body?.innerText||"").slice(0,65536)})`
	if err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expression, "returnByValue": true,
	}, &result); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result.Result.Value)
	return string(encoded), err
}

type snapshotResult struct {
	Strings   []string           `json:"strings"`
	Documents []snapshotDocument `json:"documents"`
}

type rareStringData struct {
	Index []int `json:"index"`
	Value []int `json:"value"`
}

type snapshotDocument struct {
	Nodes struct {
		ParentIndex []int          `json:"parentIndex"`
		NodeType    []int          `json:"nodeType"`
		NodeName    []int          `json:"nodeName"`
		NodeValue   []int          `json:"nodeValue"`
		Attributes  [][]int        `json:"attributes"`
		TextValue   rareStringData `json:"textValue"`
		InputValue  rareStringData `json:"inputValue"`
	} `json:"nodes"`
	Layout struct {
		NodeIndex []int       `json:"nodeIndex"`
		Bounds    [][]float64 `json:"bounds"`
		Styles    [][]int     `json:"styles"`
	} `json:"layout"`
}

type domElement struct {
	Ref        int               `json:"ref"`
	ParentRef  int               `json:"-"`
	Tag        string            `json:"tag"`
	Text       string            `json:"text,omitempty"`
	Box        [4]float64        `json:"-"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

var allowedAttributes = map[string]bool{
	"id": true, "role": true, "name": true, "type": true, "href": true,
	"value": true, "placeholder": true, "aria-label": true, "alt": true, "title": true,
}

// interactiveTags are always serialized even without text or attributes, so
// the model can click or fill them by ref.
var interactiveTags = map[string]bool{
	"a": true, "button": true, "input": true, "select": true, "textarea": true,
	"option": true, "summary": true, "label": true, "img": true, "iframe": true,
	"video": true, "audio": true,
}

// densify drops layout-only elements that carry no information for the model:
// no text, no allow-listed attributes, and a non-interactive tag.
func densify(elements []domElement) []domElement {
	dense := make([]domElement, 0, len(elements))
	for _, element := range elements {
		if element.Text == "" && len(element.Attributes) == 0 && !interactiveTags[element.Tag] {
			continue
		}
		dense = append(dense, element)
	}
	return dense
}

const hintMissNote = "selectorHint matched nothing; it is a substring filter, not a CSS selector; showing unfiltered elements"

// DOM returns dense rendered elements and their stable screen-space boxes.
// The serialized result carries the page URL and title; layout-only elements
// are omitted from the JSON but keep server-side boxes for ref clicks.
func (c *CDP) DOM(ctx context.Context, hint string, page, capBytes int) (string, map[int][4]float64, error) {
	var snapshot snapshotResult
	if err := c.call(ctx, "DOMSnapshot.captureSnapshot", map[string]any{
		"computedStyles":    []string{"display", "visibility"},
		"includePaintOrder": true,
		"includeDOMRects":   true,
	}, &snapshot); err != nil {
		return "", nil, err
	}
	elements, boxes := compactSnapshot(snapshot)
	info := c.pageInfo(ctx)
	for index := range elements {
		box := elements[index].Box
		box[0] = box[0] - info.ScrollX + info.OffsetX
		box[1] = box[1] - info.ScrollY + info.OffsetY
		elements[index].Box = box
		boxes[elements[index].Ref] = box
	}
	filtered, note := elements, ""
	if hint != "" {
		filtered = filterByHint(elements, hint)
		if len(filtered) == 0 {
			filtered, note = elements, hintMissNote
		}
	}
	if page < 0 {
		page = 0
	}
	result := paginateDOM(densify(filtered), page, capBytes)
	result["url"] = info.URL
	result["title"] = info.Title
	if note != "" {
		result["note"] = note
	}
	encoded, err := json.Marshal(result)
	return string(encoded), boxes, err
}

// filterByHint keeps elements whose serialized form (or an ancestor's)
// contains the case-insensitive hint substring.
func filterByHint(elements []domElement, hint string) []domElement {
	needle := strings.ToLower(hint)
	matches := make(map[int]bool)
	for _, element := range elements {
		encoded, _ := json.Marshal(element)
		if strings.Contains(strings.ToLower(string(encoded)), needle) {
			matches[element.Ref] = true
		}
	}
	filtered := make([]domElement, 0)
	parents := make(map[int]int, len(elements))
	for _, element := range elements {
		parents[element.Ref] = element.ParentRef
	}
	for _, element := range elements {
		for ref := element.Ref; ref >= 0; ref = parents[ref] {
			if matches[ref] {
				filtered = append(filtered, element)
				break
			}
			parent, exists := parents[ref]
			if !exists || parent == ref {
				break
			}
		}
	}
	return filtered
}

type pageInfo struct {
	URL     string  `json:"url"`
	Title   string  `json:"title"`
	Ready   bool    `json:"ready"`
	OffsetX float64 `json:"offsetX"`
	OffsetY float64 `json:"offsetY"`
	ScrollX float64 `json:"scrollX"`
	ScrollY float64 `json:"scrollY"`
}

func (c *CDP) pageInfo(ctx context.Context) pageInfo {
	var result struct {
		Result struct {
			Value pageInfo `json:"value"`
		} `json:"result"`
	}
	expression := `({url:location.href,title:document.title,ready:document.readyState==="complete",` +
		`offsetX:screenX+(outerWidth-innerWidth)/2,offsetY:screenY+(outerHeight-innerHeight),scrollX,scrollY})`
	if c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expression, "returnByValue": true,
	}, &result) != nil {
		return pageInfo{}
	}
	return result.Result.Value
}

// WaitSettled polls read-only page state after an OS-level navigation until
// the document settles: readyState complete on a new URL, or an observed
// loading transition back to complete on the same URL. On timeout it reports
// the last-seen state with Ready=false.
func (c *CDP) WaitSettled(ctx context.Context, beforeURL string, timeout time.Duration) pageInfo {
	deadline := time.Now().Add(timeout)
	sawTransition := false
	last := pageInfo{}
	for {
		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info := c.pageInfo(pollCtx)
		cancel()
		if info.URL != "" {
			last = info
			if !info.Ready {
				sawTransition = true
			}
			if info.Ready && (info.URL != beforeURL || sawTransition) {
				return info
			}
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			last.Ready = false
			return last
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func stringAt(stringsTable []string, index int) string {
	if index >= 0 && index < len(stringsTable) {
		return stringsTable[index]
	}
	return ""
}

func rareAt(data rareStringData, node int, stringsTable []string) string {
	for index, candidate := range data.Index {
		if candidate == node && index < len(data.Value) {
			return stringAt(stringsTable, data.Value[index])
		}
	}
	return ""
}

func compactSnapshot(snapshot snapshotResult) ([]domElement, map[int][4]float64) {
	elements := make([]domElement, 0)
	boxes := make(map[int][4]float64)
	refOffset := 0
	for _, document := range snapshot.Documents {
		childText := make(map[int][]string)
		for node, nodeType := range document.Nodes.NodeType {
			if nodeType == 3 {
				value := stringAt(snapshot.Strings, document.Nodes.NodeValue[node])
				if parent := document.Nodes.ParentIndex[node]; parent >= 0 {
					childText[parent] = append(childText[parent], value)
				}
			}
		}
		for layoutIndex, node := range document.Layout.NodeIndex {
			if node < 0 || node >= len(document.Nodes.NodeName) ||
				layoutIndex >= len(document.Layout.Bounds) || len(document.Layout.Bounds[layoutIndex]) < 4 {
				continue
			}
			bounds := document.Layout.Bounds[layoutIndex]
			if bounds[2] <= 0 || bounds[3] <= 0 {
				continue
			}
			if layoutIndex < len(document.Layout.Styles) {
				styles := document.Layout.Styles[layoutIndex]
				if len(styles) > 0 && stringAt(snapshot.Strings, styles[0]) == "none" {
					continue
				}
				if len(styles) > 1 && stringAt(snapshot.Strings, styles[1]) == "hidden" {
					continue
				}
			}
			tag := strings.ToLower(stringAt(snapshot.Strings, document.Nodes.NodeName[node]))
			if tag == "" || strings.HasPrefix(tag, "#") {
				continue
			}
			attributes := make(map[string]string)
			if node < len(document.Nodes.Attributes) {
				flat := document.Nodes.Attributes[node]
				for index := 0; index+1 < len(flat); index += 2 {
					name := strings.ToLower(stringAt(snapshot.Strings, flat[index]))
					if allowedAttributes[name] {
						value := stringAt(snapshot.Strings, flat[index+1])
						if len(value) > 1024 {
							value = value[:1024]
						}
						attributes[name] = value
					}
				}
			}
			if value := rareAt(document.Nodes.InputValue, node, snapshot.Strings); value != "" {
				attributes["value"] = value
			}
			text := strings.Join(childText[node], " ")
			if value := rareAt(document.Nodes.TextValue, node, snapshot.Strings); value != "" {
				text += " " + value
			}
			text = strings.Join(strings.Fields(text), " ")
			if len(text) > 512 {
				text = text[:512]
			}
			box := [4]float64{bounds[0], bounds[1], bounds[2], bounds[3]}
			ref := refOffset + node
			parentRef := -1
			if node < len(document.Nodes.ParentIndex) && document.Nodes.ParentIndex[node] >= 0 {
				parentRef = refOffset + document.Nodes.ParentIndex[node]
			}
			boxes[ref] = box
			element := domElement{Ref: ref, ParentRef: parentRef, Tag: tag, Text: text, Box: box}
			if len(attributes) > 0 {
				element.Attributes = attributes
			}
			elements = append(elements, element)
		}
		refOffset += len(document.Nodes.NodeName)
	}
	return elements, boxes
}

func paginateDOM(elements []domElement, page, capBytes int) map[string]any {
	if capBytes <= 0 {
		capBytes = domCap
	}
	start := 0
	for current := 0; current < page; current++ {
		_, next := domPage(elements, start, capBytes)
		if next <= start {
			start = len(elements)
			break
		}
		start = next
	}
	items, next := domPage(elements, start, capBytes)
	result := map[string]any{"page": page, "elements": items}
	if next < len(elements) {
		result["more"] = map[string]any{"nextPage": page + 1, "remaining": len(elements) - next}
	}
	return result
}

func domPage(elements []domElement, start, capBytes int) ([]domElement, int) {
	items := make([]domElement, 0)
	index := start
	for ; index < len(elements); index++ {
		candidate := append(items, elements[index])
		encoded, _ := json.Marshal(map[string]any{"elements": candidate, "more": map[string]int{"nextPage": 999999, "remaining": len(elements)}})
		if len(encoded) > capBytes && len(items) > 0 {
			break
		}
		items = candidate
	}
	return items, index
}
