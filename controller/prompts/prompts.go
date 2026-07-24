// Package prompts embeds the controller's reviewable system-prompt sources.
package prompts

import _ "embed"

// Agent is the browser agent's system-prompt template.
//
//go:embed agent-system.txt
var Agent string

// Chat is the fallback chat system prompt.
//
//go:embed chat-system.txt
var Chat string
