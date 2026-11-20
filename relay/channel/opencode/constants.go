package opencode

const ChannelName = "opencode"

// ModelList is empty because OpenCode models are fetched dynamically from the
// upstream /v1/models endpoint.
var ModelList = []string{}

const (
	chatCompletionsPath      = "/v1/chat/completions"
	responsesPath            = "/v1/responses"
	claudeMessagesPath       = "/v1/messages"
	geminiGeneratePathPrefix = "/v1beta/models/"

	opencodeChatCompletionsPath      = "/openai/v1/chat/completions"
	opencodeResponsesPath            = "/openai/v1/responses"
	opencodeClaudeMessagesPath       = "/anthropic/v1/messages"
	opencodeGeminiGeneratePathPrefix = "/google/v1beta/models/"
)

// clientHeaders are the client headers OpenCode uses for session routing and
// client identification. Forwarding them keeps prompt cache affinity and lets
// OpenCode Go recognize the original coding agent instead of a generic gateway.
var clientHeaders = []string{
	"User-Agent",
	"x-opencode-session",
	"x-opencode-session-id",
	"x-opencode-parent-session-id",
	"x-opencode-project",
	"x-opencode-client",
	"x-session-affinity",
	"x-session-id",
	"session_id",
	"conversation_id",
	"x-claude-code-session-id",
	"x-codex-session-id",
}

// sessionHeaders are checked in order for a client-provided conversation ID.
var sessionHeaders = []string{
	"x-opencode-session",
	"x-opencode-session-id",
	"x-session-affinity",
	"x-session-id",
	"session_id",
	"conversation_id",
	"x-claude-code-session-id",
	"x-codex-session-id",
}
