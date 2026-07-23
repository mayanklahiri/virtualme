package chat

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// valkeyClient is a minimal RESP client implementing only the commands the
// chat service uses. Each operation dials a fresh connection with deadlines
// so a stuck Valkey can never wedge the controller.
type valkeyClient struct {
	addr    string
	timeout time.Duration
}

func newValkeyClient(addr string) *valkeyClient {
	return &valkeyClient{addr: addr, timeout: 2 * time.Second}
}

func (v *valkeyClient) do(args ...string) (any, error) {
	conn, err := net.DialTimeout("tcp", v.addr, v.timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(v.timeout)); err != nil {
		return nil, err
	}

	request := make([]byte, 0, 64)
	request = fmt.Appendf(request, "*%d\r\n", len(args))
	for _, arg := range args {
		request = fmt.Appendf(request, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	return parseReply(bufio.NewReader(conn))
}

func parseReply(reader *bufio.Reader) (any, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, fmt.Errorf("valkey: short reply %q", line)
	}
	body := line[1 : len(line)-2]
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return nil, fmt.Errorf("valkey: %s", body)
	case ':':
		return strconv.ParseInt(body, 10, 64)
	case '$':
		length, err := strconv.Atoi(body)
		if err != nil {
			return nil, err
		}
		if length < 0 {
			return nil, nil // nil bulk string
		}
		payload := make([]byte, length+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		return string(payload[:length]), nil
	case '*':
		count, err := strconv.Atoi(body)
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, nil
		}
		items := make([]any, count)
		for index := range items {
			if items[index], err = parseReply(reader); err != nil {
				return nil, err
			}
		}
		return items, nil
	default:
		return nil, fmt.Errorf("valkey: unknown reply type %q", line[0])
	}
}

func (v *valkeyClient) rpush(key, value string) error {
	_, err := v.do("RPUSH", key, value)
	return err
}

func (v *valkeyClient) ltrim(key string, start, stop int) error {
	_, err := v.do("LTRIM", key, strconv.Itoa(start), strconv.Itoa(stop))
	return err
}

func (v *valkeyClient) lrange(key string, start, stop int) ([]string, error) {
	reply, err := v.do("LRANGE", key, strconv.Itoa(start), strconv.Itoa(stop))
	if err != nil {
		return nil, err
	}
	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("valkey: LRANGE reply is %T, want array", reply)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result, nil
}

func (v *valkeyClient) del(keys ...string) error {
	_, err := v.do(append([]string{"DEL"}, keys...)...)
	return err
}

func (v *valkeyClient) hincrby(key, field string, value int64) error {
	_, err := v.do("HINCRBY", key, field, strconv.FormatInt(value, 10))
	return err
}

func (v *valkeyClient) hgetall(key string) (map[string]int64, error) {
	reply, err := v.do("HGETALL", key)
	if err != nil {
		return nil, err
	}
	items, ok := reply.([]any)
	if !ok || len(items)%2 != 0 {
		return nil, fmt.Errorf("valkey: HGETALL reply is %T, want even array", reply)
	}
	result := make(map[string]int64, len(items)/2)
	for i := 0; i < len(items); i += 2 {
		field, fieldOK := items[i].(string)
		value, valueOK := items[i+1].(string)
		number, parseErr := strconv.ParseInt(value, 10, 64)
		if fieldOK && valueOK && parseErr == nil {
			result[field] = number
		}
	}
	return result, nil
}
