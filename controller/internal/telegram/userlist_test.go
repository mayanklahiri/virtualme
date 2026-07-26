package telegram

import (
	"encoding/json"
	"testing"
)

type userlistStore struct {
	values map[string]string
}

func (s *userlistStore) Get(key string) (*string, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, nil
	}
	copy := value
	return &copy, nil
}

func (s *userlistStore) Set(key, value string) error {
	s.values[key] = value
	return nil
}

func (s *userlistStore) LRange(string, int, int) ([]string, error) { return nil, nil }
func (s *userlistStore) LLen(string) (int64, error)                { return 0, nil }
func (s *userlistStore) Eval(string, []string, ...string) (any, error) {
	return nil, nil
}
func (s *userlistStore) HGetAll(string) (map[string]string, error) { return nil, nil }
func (s *userlistStore) HSet(string, ...string) (int64, error)     { return 0, nil }
func (s *userlistStore) HDel(string, ...string) (int64, error)     { return 0, nil }

func TestNormalizeUserIDs(t *testing.T) {
	values, err := normalizeUserIDs([]string{" 42 ", "42", "7"})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != "7" || values[1] != "42" {
		t.Fatalf("normalizeUserIDs()=%v", values)
	}
	if _, err := normalizeUserIDs([]string{"0"}); err == nil {
		t.Fatal("expected invalid user ID")
	}
}

func TestMigrateAndAuthorizeAllowedUsers(t *testing.T) {
	store := &userlistStore{values: map[string]string{}}
	service := New(Config{AllowedChatIDs: []string{"-100"}}, store, func([]byte) {}, nil)
	if err := service.MigrateAllowedUsers([]string{"9", "9", "42"}); err != nil {
		t.Fatal(err)
	}
	if !service.isAuthorized("-100", "42", false) {
		t.Fatal("expected authorized user")
	}
	if service.isAuthorized("-100", "99", false) {
		t.Fatal("expected denied user")
	}
	raw := store.values[allowedUsersKey]
	var persisted []string
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 || persisted[0] != "9" || persisted[1] != "42" {
		t.Fatalf("persisted=%v", persisted)
	}
}

func TestEmptyAllowedUsersPermitsHumans(t *testing.T) {
	service := New(Config{AllowedChatIDs: []string{"-100"}}, &userlistStore{values: map[string]string{}}, func([]byte) {}, nil)
	if err := service.MigrateAllowedUsers(nil); err != nil {
		t.Fatal(err)
	}
	if !service.isAuthorized("-100", "999", false) {
		t.Fatal("expected empty allowlist to permit human sender")
	}
}
