package telegram

import "encoding/json"

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

type Message struct {
	MessageID int64           `json:"message_id"`
	Date      int64           `json:"date"`
	Text      string          `json:"text"`
	Chat      *Chat           `json:"chat"`
	From      *User           `json:"from"`
	Entities  []MessageEntity `json:"entities"`
}

type Update struct {
	UpdateID      int64           `json:"update_id"`
	Message       *Message        `json:"message,omitempty"`
	EditedMessage *Message        `json:"edited_message,omitempty"`
	Raw           json.RawMessage `json:"-"`
}

func (u *Update) UnmarshalJSON(raw []byte) error {
	type plain Update
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*u = Update(value)
	u.Raw = append(u.Raw[:0], raw...)
	return nil
}

type GetUpdatesRequest struct {
	Offset         int64    `json:"offset"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

type SendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type SendChatActionRequest struct {
	ChatID string `json:"chat_id"`
	Action string `json:"action"`
}

type ResponseParameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type Response[T any] struct {
	OK          bool               `json:"ok"`
	Result      T                  `json:"result"`
	ErrorCode   int                `json:"error_code,omitempty"`
	Description string             `json:"description,omitempty"`
	Parameters  ResponseParameters `json:"parameters,omitempty"`
}

type Config struct {
	Enabled            bool
	BotToken           string
	AllowedChatIDs     []string
	AllowedUserIDs     []string
	PollTimeoutSeconds int
	MaxEventLog        int
}

type BotIdentity struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

type Destination struct {
	ChatID   string `json:"chatId"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Observed bool   `json:"observed"`
}

type PollStatus struct {
	TimeoutSeconds      int    `json:"timeoutSeconds"`
	NextOffset          int64  `json:"nextOffset"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	RetryAt             *int64 `json:"retryAt"`
	LastSuccessTs       int64  `json:"lastSuccessTs"`
}

type Status struct {
	Enabled      bool          `json:"enabled"`
	State        string        `json:"state"`
	Code         string        `json:"code"`
	Detail       string        `json:"detail"`
	Bot          BotIdentity   `json:"bot"`
	Poll         PollStatus    `json:"poll"`
	Destinations []Destination `json:"destinations"`
	EventCount   int           `json:"eventCount"`
	MaxEventLog  int           `json:"maxEventLog"`
}

type Event struct {
	ID          string          `json:"id"`
	Ts          int64           `json:"ts"`
	UpdateID    int64           `json:"updateId"`
	Kind        string          `json:"kind"`
	Outcome     string          `json:"outcome"`
	ChatID      string          `json:"chatId,omitempty"`
	ChatType    string          `json:"chatType,omitempty"`
	ChatLabel   string          `json:"chatLabel,omitempty"`
	UserID      string          `json:"userId,omitempty"`
	Username    string          `json:"username,omitempty"`
	MessageID   int64           `json:"messageId,omitempty"`
	TextPreview string          `json:"textPreview"`
	Detail      string          `json:"detail"`
	RawUpdate   json.RawMessage `json:"rawUpdate"`
	RawOmitted  bool            `json:"rawOmitted"`
}
