package origin

type Source struct {
	Channel  string `json:"channel"`
	ChatID   string `json:"chatId,omitempty"`
	UserID   string `json:"userId,omitempty"`
	UpdateID int64  `json:"updateId,omitempty"`
}
