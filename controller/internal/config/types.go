package config

type Config struct {
	Version      int                `json:"version"`
	Server       ServerConfig       `json:"server"`
	Desktop      DesktopConfig      `json:"desktop"`
	Valkey       ValkeyConfig       `json:"valkey"`
	Llama        LlamaConfig        `json:"llama"`
	TTS          TTSConfig          `json:"tts"`
	Agent        AgentConfig        `json:"agent"`
	Mail         MailConfig         `json:"mail"`
	Health       HealthConfig       `json:"health"`
	Integrations IntegrationsConfig `json:"integrations"`
}

type ServerConfig struct {
	HTTPAddress     string `json:"httpAddress"`
	DesktopProxyURL string `json:"desktopProxyURL"`
}

type DesktopConfig struct {
	Display               string `json:"display"`
	Resolution            string `json:"resolution"`
	X11SocketDirectory    string `json:"x11SocketDirectory"`
	VNCAddress            string `json:"vncAddress"`
	NoVNCAddress          string `json:"noVNCAddress"`
	NoVNCUpstreamAddress  string `json:"noVNCUpstreamAddress"`
	NoVNCHealthURL        string `json:"noVNCHealthURL"`
	CDPURL                string `json:"cdpURL"`
	ChromiumWatchdogGrace int    `json:"chromiumWatchdogGrace"`
}

type ValkeyConfig struct {
	Address string `json:"address"`
}

type LlamaConfig struct {
	Address            string `json:"address"`
	ContextTokens      int    `json:"contextTokens"`
	ModelPath          string `json:"modelPath"`
	ProjectorPath      string `json:"projectorPath"`
	Threads            int    `json:"threads"`
	ChatCompletionsURL string `json:"chatCompletionsURL"`
}

type TTSConfig struct {
	Address         string `json:"address"`
	HealthURL       string `json:"healthURL"`
	SherpaDirectory string `json:"sherpaDirectory"`
	ModelDirectory  string `json:"modelDirectory"`
	CacheDirectory  string `json:"cacheDirectory"`
	CacheMaxMiB     int64  `json:"cacheMaxMiB"`
	MaxCharacters   int    `json:"maxCharacters"`
}

type AgentConfig struct {
	MaxSteps           int    `json:"maxSteps"`
	KeepTasks          int    `json:"keepTasks"`
	XdotoolPath        string `json:"xdotoolPath"`
	ScrotPath          string `json:"scrotPath"`
	ConvertPath        string `json:"convertPath"`
	BashPath           string `json:"bashPath"`
	SystemManifestPath string `json:"systemManifestPath"`
}

type MailConfig struct {
	Mailname       string          `json:"mailname"`
	From           string          `json:"from"`
	Smarthost      SmarthostConfig `json:"smarthost"`
	DKIMDomain     string          `json:"dkimDomain"`
	DKIMSelector   string          `json:"dkimSelector"`
	FlushSeconds   int             `json:"flushSeconds"`
	SendmailPath   string          `json:"sendmailPath"`
	SpoolDirectory string          `json:"spoolDirectory"`
}

type SmarthostConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type HealthConfig struct {
	LlamaURL    string `json:"llamaURL"`
	XdotoolPath string `json:"xdotoolPath"`
}

type IntegrationsConfig struct {
	Telegram TelegramConfig `json:"telegram"`
}

type TelegramConfig struct {
	Enabled            bool     `json:"enabled"`
	BotToken           string   `json:"botToken"`
	AllowedChatIDs     []string `json:"allowedChatIds"`
	AllowedUserIDs     []string `json:"allowedUserIds"`
	PollTimeoutSeconds int      `json:"pollTimeoutSeconds"`
	MaxEventLog        int      `json:"maxEventLog"`
}

type RawConfig map[string]any

type Source string

const (
	SourceDefault Source = "default"
	SourceYAML    Source = "yaml"
	SourceLegacy  Source = "legacy-env"
)

type Loaded struct {
	Config     Config                  `json:"-"`
	Raw        RawConfig               `json:"raw"`
	Sources    map[string]Source       `json:"sources"`
	Secrets    map[string]SecretStatus `json:"secrets"`
	Hash       string                  `json:"fileHash"`
	SchemaHash string                  `json:"schemaHash"`
	DataDir    string                  `json:"-"`
	File       string                  `json:"-"`
}
