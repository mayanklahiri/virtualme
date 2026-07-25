// Package valkey implements the controller's small, dependency-free RESP2
// client. Each command uses a fresh connection with a deadline.
package valkey

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Client is a minimal RESP2 client.
type Client struct {
	addr    string
	timeout time.Duration
}

// New returns a client for addr.
func New(addr string) *Client {
	return &Client{addr: addr, timeout: 2 * time.Second}
}

func (v *Client) do(args ...string) (any, error) {
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
			return nil, nil
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

func expectInt(command string, reply any) (int64, error) {
	value, ok := reply.(int64)
	if !ok {
		return 0, fmt.Errorf("valkey: %s reply is %T, want integer", command, reply)
	}
	return value, nil
}

func expectStrings(command string, reply any) ([]string, error) {
	items, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("valkey: %s reply is %T, want array", command, reply)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("valkey: %s array item is %T, want string", command, item)
		}
		result = append(result, text)
	}
	return result, nil
}

// RPush appends values to a list.
func (v *Client) RPush(key string, values ...string) (int64, error) {
	reply, err := v.do(append([]string{"RPUSH", key}, values...)...)
	if err != nil {
		return 0, err
	}
	return expectInt("RPUSH", reply)
}

// LPush prepends values to a list.
func (v *Client) LPush(key string, values ...string) (int64, error) {
	reply, err := v.do(append([]string{"LPUSH", key}, values...)...)
	if err != nil {
		return 0, err
	}
	return expectInt("LPUSH", reply)
}

// LTrim limits a list to the inclusive range.
func (v *Client) LTrim(key string, start, stop int) error {
	_, err := v.do("LTRIM", key, strconv.Itoa(start), strconv.Itoa(stop))
	return err
}

// LRange reads an inclusive list range.
func (v *Client) LRange(key string, start, stop int) ([]string, error) {
	reply, err := v.do("LRANGE", key, strconv.Itoa(start), strconv.Itoa(stop))
	if err != nil {
		return nil, err
	}
	return expectStrings("LRANGE", reply)
}

// LLen returns a list's length.
func (v *Client) LLen(key string) (int64, error) {
	reply, err := v.do("LLEN", key)
	if err != nil {
		return 0, err
	}
	return expectInt("LLEN", reply)
}

// LRem removes count exact list values.
func (v *Client) LRem(key string, count int, value string) (int64, error) {
	reply, err := v.do("LREM", key, strconv.Itoa(count), value)
	if err != nil {
		return 0, err
	}
	return expectInt("LREM", reply)
}

// LMove atomically moves one list value and returns nil when src is empty.
func (v *Client) LMove(src, dst, from, to string) (*string, error) {
	reply, err := v.do("LMOVE", src, dst, from, to)
	if err != nil || reply == nil {
		return nil, err
	}
	text, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("valkey: LMOVE reply is %T, want bulk string", reply)
	}
	return &text, nil
}

// Del removes keys.
func (v *Client) Del(keys ...string) (int64, error) {
	reply, err := v.do(append([]string{"DEL"}, keys...)...)
	if err != nil {
		return 0, err
	}
	return expectInt("DEL", reply)
}

// HIncrBy increments a hash field.
func (v *Client) HIncrBy(key, field string, value int64) (int64, error) {
	reply, err := v.do("HINCRBY", key, field, strconv.FormatInt(value, 10))
	if err != nil {
		return 0, err
	}
	return expectInt("HINCRBY", reply)
}

// HGetAll returns all hash fields as strings.
func (v *Client) HGetAll(key string) (map[string]string, error) {
	reply, err := v.do("HGETALL", key)
	if err != nil {
		return nil, err
	}
	items, err := expectStrings("HGETALL", reply)
	if err != nil || len(items)%2 != 0 {
		return nil, fmt.Errorf("valkey: HGETALL reply must be an even string array")
	}
	result := make(map[string]string, len(items)/2)
	for i := 0; i < len(items); i += 2 {
		result[items[i]] = items[i+1]
	}
	return result, nil
}

// HSet sets variadic field/value pairs.
func (v *Client) HSet(key string, fieldValues ...string) (int64, error) {
	if len(fieldValues)%2 != 0 {
		return 0, fmt.Errorf("valkey: HSET requires field/value pairs")
	}
	reply, err := v.do(append([]string{"HSET", key}, fieldValues...)...)
	if err != nil {
		return 0, err
	}
	return expectInt("HSET", reply)
}

// HGet reads one hash field.
func (v *Client) HGet(key, field string) (*string, error) {
	reply, err := v.do("HGET", key, field)
	if err != nil || reply == nil {
		return nil, err
	}
	value, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("valkey: HGET reply is %T, want bulk string", reply)
	}
	return &value, nil
}

// HDel removes hash fields.
func (v *Client) HDel(key string, fields ...string) (int64, error) {
	reply, err := v.do(append([]string{"HDEL", key}, fields...)...)
	if err != nil {
		return 0, err
	}
	return expectInt("HDEL", reply)
}

// Set stores one string value.
func (v *Client) Set(key, value string) error {
	_, err := v.do("SET", key, value)
	return err
}

// SetEx stores one string value with a TTL in seconds.
func (v *Client) SetEx(key, value string, seconds int) error {
	_, err := v.do("SET", key, value, "EX", strconv.Itoa(seconds))
	return err
}

// Get reads one string value.
func (v *Client) Get(key string) (*string, error) {
	reply, err := v.do("GET", key)
	if err != nil || reply == nil {
		return nil, err
	}
	value, ok := reply.(string)
	if !ok {
		return nil, fmt.Errorf("valkey: GET reply is %T, want bulk string", reply)
	}
	return &value, nil
}
