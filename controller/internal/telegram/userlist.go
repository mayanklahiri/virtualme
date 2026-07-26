package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mayanklahiri/virtualme/controller/internal/ws"
)

const allowedUsersKey = "virtualme:telegram:allowed-users"

var canonicalUserID = regexp.MustCompile(`^[1-9][0-9]*$`)

func normalizeUserIDs(values []string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !canonicalUserID.MatchString(value) {
			return nil, fmt.Errorf("invalid Telegram user ID %q", value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _ := strconv.ParseInt(result[i], 10, 64)
		right, _ := strconv.ParseInt(result[j], 10, 64)
		return left < right
	})
	return result, nil
}

func (s *Service) allowedUsersLocked() []string {
	if len(s.allowedUserIDs) == 0 {
		return nil
	}
	out := make([]string, len(s.allowedUserIDs))
	copy(out, s.allowedUserIDs)
	return out
}

func (s *Service) isAuthorized(chatID, userID string, isBot bool) bool {
	s.mu.Lock()
	userIDs := s.allowedUsersLocked()
	chatIDs := append([]string(nil), s.config.AllowedChatIDs...)
	s.mu.Unlock()
	return AuthorizeSender(chatIDs, userIDs, chatID, userID, isBot)
}

func (s *Service) loadAllowedUsers() error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	raw, err := s.store.Get(allowedUsersKey)
	if err != nil {
		return err
	}
	if raw == nil {
		s.mu.Lock()
		s.allowedUserIDs = nil
		s.mu.Unlock()
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(*raw), &values); err != nil {
		return fmt.Errorf("decode allowed users: %w", err)
	}
	normalized, err := normalizeUserIDs(values)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.allowedUserIDs = normalized
	s.mu.Unlock()
	return nil
}

func (s *Service) persistAllowedUsers(values []string) error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return s.store.Set(allowedUsersKey, string(payload))
}

// MigrateAllowedUsers seeds Valkey from legacy config when unset.
func (s *Service) MigrateAllowedUsers(legacy []string) error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	raw, err := s.store.Get(allowedUsersKey)
	if err != nil {
		return err
	}
	if raw != nil {
		return s.loadAllowedUsers()
	}
	normalized, err := normalizeUserIDs(legacy)
	if err != nil {
		return err
	}
	if err := s.persistAllowedUsers(normalized); err != nil {
		return err
	}
	s.mu.Lock()
	s.allowedUserIDs = normalized
	s.mu.Unlock()
	return nil
}

func (s *Service) AllowedUsersMessage() []byte {
	s.mu.Lock()
	users := append([]string(nil), s.allowedUserIDs...)
	s.mu.Unlock()
	body, _ := json.Marshal(map[string]any{"type": "telegram-userlist", "userIds": users})
	return body
}

func (s *Service) handleUserListSet(conn *ws.Conn, payload []byte) {
	var request struct {
		Type      string   `json:"type"`
		RequestID string   `json:"requestId"`
		UserIDs   []string `json:"userIds"`
	}
	errorText := ""
	if decodeStrict(payload, &request) != nil || !requestID.MatchString(request.RequestID) {
		return
	}
	normalized, err := normalizeUserIDs(request.UserIDs)
	if err != nil {
		errorText = err.Error()
	} else if err := s.persistAllowedUsers(normalized); err != nil {
		errorText = "Unable to save user allowlist"
	} else {
		s.mu.Lock()
		s.allowedUserIDs = normalized
		s.mu.Unlock()
	}
	frame, _ := json.Marshal(map[string]any{
		"type": "telegram-command-result", "id": request.RequestID,
		"ok": errorText == "", "error": errorText,
	})
	_ = conn.WriteText(frame)
	if errorText == "" {
		s.broadcast(s.AllowedUsersMessage())
	}
}
