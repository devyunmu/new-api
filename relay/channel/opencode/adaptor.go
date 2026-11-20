package opencode

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

// Adaptor relays to OpenCode Console / Zen / Go. OpenCode serves every text
// model through each of its native protocol families, so the incoming format is
// forwarded to the matching endpoint without cross-protocol conversion.
type Adaptor struct {
	openaiAdaptor openai.Adaptor
	claudeAdaptor claude.Adaptor
	geminiAdaptor gemini.Adaptor

	// fallbackSession is derived from the conversation prefix when the client
	// sends no session header, so retries and later turns keep one session.
	fallbackSession string
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.openaiAdaptor.Init(info)
	a.claudeAdaptor.Init(info)
	a.geminiAdaptor.Init(info)
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	root, legacy, err := endpointRoot(info.ChannelBaseUrl)
	if err != nil {
		return "", err
	}

	switch info.RelayFormat {
	case types.RelayFormatClaude:
		path := claudeMessagesPath
		if !legacy {
			path = opencodeClaudeMessagesPath
		}
		return root + path, nil
	case types.RelayFormatGemini:
		if legacy {
			return "", errors.New("opencode: the legacy /zen base URL does not support Gemini; use https://opencode.ai/inference")
		}
		model := info.UpstreamModelName
		if model == "" {
			model = info.OriginModelName
		}
		if model == "" {
			return "", errors.New("opencode: gemini request has no model")
		}
		action := "generateContent"
		prefix := geminiGeneratePathPrefix
		if !legacy {
			prefix = opencodeGeminiGeneratePathPrefix
		}
		if info.IsStream {
			action = "streamGenerateContent?alt=sse"
			info.DisablePing = true
		}
		return root + prefix + model + ":" + action, nil
	case types.RelayFormatOpenAIResponses:
		if info.RelayMode == relayconstant.RelayModeResponsesCompact {
			return "", errors.New("opencode: the responses compact endpoint is not supported")
		}
		return root + responsesEndpointPath(legacy), nil
	case types.RelayFormatEmbedding, types.RelayFormatRerank, types.RelayFormatOpenAIImage,
		types.RelayFormatOpenAIAudio, types.RelayFormatOpenAIRealtime, types.RelayFormatDeepSeekTools:
		return "", fmt.Errorf("opencode: relay format %s is not supported", info.RelayFormat)
	}

	if info.RelayMode == relayconstant.RelayModeChatCompletions || info.RelayMode == relayconstant.RelayModeUnknown {
		path := chatCompletionsPath
		if !legacy {
			path = opencodeChatCompletionsPath
		}
		return root + path, nil
	}
	if info.RelayMode == relayconstant.RelayModeResponses {
		return root + responsesEndpointPath(legacy), nil
	}
	return "", fmt.Errorf("opencode: relay mode %d is not supported", info.RelayMode)
}

func responsesEndpointPath(legacy bool) string {
	if legacy {
		return responsesPath
	}
	return opencodeResponsesPath
}

// endpointRoot returns the upstream base and whether the base uses the legacy
// /zen layout. OpenCode accepts both layouts:
//
//   - legacy: https://opencode.ai/zen[/go] with /v1/... paths
//   - current: https://opencode.ai/inference[/go] with /openai/v1/... and
//     /anthropic/v1/... paths
//
// A bare opencode.ai address defaults to the current pay-as-you-go base.
func endpointRoot(rawBaseURL string) (string, bool, error) {
	rawBaseURL = strings.TrimSpace(rawBaseURL)
	if rawBaseURL == "" {
		rawBaseURL = constant.GetChannelBaseURL(constant.ChannelTypeOpenCode)
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return "", false, fmt.Errorf("opencode: invalid base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return "", false, errors.New("opencode: base URL must use http or https")
	}
	if baseURL.Host == "" {
		return "", false, errors.New("opencode: base URL must include a host")
	}

	path := strings.TrimRight(baseURL.Path, "/")
	if path == "" && strings.EqualFold(baseURL.Hostname(), "opencode.ai") {
		path = "/inference"
	}
	legacy := path == "/zen" || strings.HasPrefix(path, "/zen/")
	baseURL.Path = path
	baseURL.RawPath = ""
	baseURL.RawQuery = ""
	baseURL.Fragment = ""
	return strings.TrimRight(baseURL.String(), "/"), legacy, nil
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	header.Set("Authorization", "Bearer "+info.ApiKey)

	clientHeader := http.Header(nil)
	if c != nil && c.Request != nil {
		clientHeader = c.Request.Header
	}
	for _, name := range clientHeaders {
		if value := strings.TrimSpace(clientHeader.Get(name)); value != "" {
			header.Set(name, value)
		}
	}

	session := ""
	for _, name := range sessionHeaders {
		if value := strings.TrimSpace(clientHeader.Get(name)); value != "" {
			session = value
			break
		}
	}
	if session == "" {
		session = a.fallbackSession
	}
	if session == "" {
		session = info.RequestId
	}
	if session != "" {
		header.Set("x-opencode-session", session)
	}

	if info.RelayFormat == types.RelayFormatClaude {
		version := strings.TrimSpace(clientHeader.Get("anthropic-version"))
		if version == "" {
			version = "2023-06-01"
		}
		header.Set("anthropic-version", version)
		if beta := strings.Join(clientHeader.Values("anthropic-beta"), ","); beta != "" {
			header.Set("anthropic-beta", beta)
		}
		// The legacy /zen Messages endpoint authenticates with x-api-key only.
		if _, legacy, err := endpointRoot(info.ChannelBaseUrl); err == nil && legacy {
			header.Del("Authorization")
			header.Set("x-api-key", info.ApiKey)
		}
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	a.rememberSession(
		request.Model,
		firstMessageContent(request.Messages, "system"),
		firstMessageContent(request.Messages, "user"),
	)
	return request, nil
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	a.rememberSession(request.Model, request.Instructions, request.Input)
	return request, nil
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	a.rememberSession(request.Model, request.System, firstClaudeMessageContent(request.Messages, "user"))
	return request, nil
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	model := info.UpstreamModelName
	if model == "" {
		model = info.OriginModelName
	}
	a.rememberSession(model, request.SystemInstructions, firstGeminiContent(request.Contents))
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("opencode: embeddings are not supported")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("opencode: audio is not supported")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("opencode: image generation is not supported")
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, errors.New("opencode: rerank is not supported")
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayFormat {
	case types.RelayFormatClaude:
		return a.claudeAdaptor.DoResponse(c, resp, info)
	case types.RelayFormatGemini:
		return a.geminiAdaptor.DoResponse(c, resp, info)
	default:
		return a.openaiAdaptor.DoResponse(c, resp, info)
	}
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}

// rememberSession derives a stable session ID from the first turns of the
// conversation when the client did not provide one. OpenCode Go rejects
// requests without x-opencode-session, and a per-request ID would lose prompt
// cache affinity between turns.
func (a *Adaptor) rememberSession(model string, parts ...any) {
	if a.fallbackSession != "" {
		return
	}
	hash := sha256.New()
	hash.Write([]byte(model))
	hash.Write([]byte{0})
	for _, part := range parts {
		hash.Write([]byte(common.GetJsonString(part)))
		hash.Write([]byte{0})
	}
	a.fallbackSession = "ses_" + hex.EncodeToString(hash.Sum(nil))[:32]
}

func firstMessageContent(messages []dto.Message, role string) any {
	for _, message := range messages {
		if message.Role == role {
			return message.Content
		}
	}
	return nil
}

func firstClaudeMessageContent(messages []dto.ClaudeMessage, role string) any {
	for _, message := range messages {
		if message.Role == role {
			return message.Content
		}
	}
	return nil
}

func firstGeminiContent(contents []dto.GeminiChatContent) any {
	if len(contents) == 0 {
		return nil
	}
	return contents[0]
}
