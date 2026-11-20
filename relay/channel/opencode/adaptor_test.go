package opencode

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRelayInfo(baseURL string, format types.RelayFormat) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat: format,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenCode,
			ChannelBaseUrl:    baseURL,
			ApiKey:            "oc_sk_test",
			UpstreamModelName: "glm-5.3",
		},
	}
}

func TestAdaptorGetRequestURLRoutesEachProtocol(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		format  types.RelayFormat
		stream  bool
		want    string
	}{
		{
			name:    "current chat completions",
			baseURL: "https://opencode.ai/inference/go",
			format:  types.RelayFormatOpenAI,
			want:    "https://opencode.ai/inference/go/openai/v1/chat/completions",
		},
		{
			name:    "legacy chat completions",
			baseURL: "https://opencode.ai/zen/go/",
			format:  types.RelayFormatOpenAI,
			want:    "https://opencode.ai/zen/go/v1/chat/completions",
		},
		{
			name:    "current responses",
			baseURL: "https://opencode.ai/inference",
			format:  types.RelayFormatOpenAIResponses,
			want:    "https://opencode.ai/inference/openai/v1/responses",
		},
		{
			name:    "legacy responses",
			baseURL: "https://opencode.ai/zen",
			format:  types.RelayFormatOpenAIResponses,
			want:    "https://opencode.ai/zen/v1/responses",
		},
		{
			name:    "current anthropic messages",
			baseURL: "https://opencode.ai/inference/go",
			format:  types.RelayFormatClaude,
			want:    "https://opencode.ai/inference/go/anthropic/v1/messages",
		},
		{
			name:    "legacy anthropic messages",
			baseURL: "https://opencode.ai/zen/go",
			format:  types.RelayFormatClaude,
			want:    "https://opencode.ai/zen/go/v1/messages",
		},
		{
			name:    "current gemini",
			baseURL: "https://opencode.ai/inference",
			format:  types.RelayFormatGemini,
			want:    "https://opencode.ai/inference/google/v1beta/models/glm-5.3:generateContent",
		},
		{
			name:    "current gemini stream",
			baseURL: "https://opencode.ai/inference",
			format:  types.RelayFormatGemini,
			stream:  true,
			want:    "https://opencode.ai/inference/google/v1beta/models/glm-5.3:streamGenerateContent?alt=sse",
		},
		{
			name:    "bare opencode host defaults to current base",
			baseURL: "https://opencode.ai",
			format:  types.RelayFormatOpenAI,
			want:    "https://opencode.ai/inference/openai/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := newRelayInfo(tt.baseURL, tt.format)
			info.IsStream = tt.stream
			got, err := (&Adaptor{}).GetRequestURL(info)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAdaptorGetRequestURLRejectsUnsupportedEndpoints(t *testing.T) {
	for _, format := range []types.RelayFormat{types.RelayFormatEmbedding, types.RelayFormatOpenAIImage, types.RelayFormatRerank} {
		info := newRelayInfo("https://opencode.ai/inference", format)
		_, err := (&Adaptor{}).GetRequestURL(info)
		assert.Error(t, err, format)
	}

	legacyGemini := newRelayInfo("https://opencode.ai/zen/go", types.RelayFormatGemini)
	_, err := (&Adaptor{}).GetRequestURL(legacyGemini)
	assert.ErrorContains(t, err, "legacy /zen base URL does not support Gemini")
}

func TestAdaptorSetupRequestHeaderForwardsClientIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "opencode/2.2.7/cli")
	c.Request.Header.Set("x-opencode-session", "ses_client")
	c.Request.Header.Set("x-opencode-client", "cli")
	c.Request.Header.Set("x-opencode-project", "project-hash")
	c.Request.Header.Add("anthropic-beta", "beta-one")
	c.Request.Header.Add("anthropic-beta", "beta-two")
	c.Request.Header.Set("Authorization", "Bearer client-secret")

	info := newRelayInfo("https://opencode.ai/inference", types.RelayFormatClaude)
	header := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))

	assert.Equal(t, "Bearer oc_sk_test", header.Get("Authorization"))
	assert.Equal(t, "opencode/2.2.7/cli", header.Get("User-Agent"))
	assert.Equal(t, "ses_client", header.Get("x-opencode-session"))
	assert.Equal(t, "cli", header.Get("x-opencode-client"))
	assert.Equal(t, "project-hash", header.Get("x-opencode-project"))
	assert.Equal(t, "2023-06-01", header.Get("anthropic-version"))
	assert.Equal(t, "beta-one,beta-two", header.Get("anthropic-beta"))
}

func TestAdaptorSetupRequestHeaderUsesLegacyClaudeAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	info := newRelayInfo("https://opencode.ai/zen/go", types.RelayFormatClaude)
	header := http.Header{}
	require.NoError(t, (&Adaptor{}).SetupRequestHeader(c, &header, info))

	assert.Empty(t, header.Get("Authorization"))
	assert.Equal(t, "oc_sk_test", header.Get("x-api-key"))
	assert.Equal(t, "2023-06-01", header.Get("anthropic-version"))
}

func TestAdaptorSetupRequestHeaderDerivesStableSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buildRequest := func(content string) *dto.GeneralOpenAIRequest {
		return &dto.GeneralOpenAIRequest{
			Model: "glm-5.3",
			Messages: []dto.Message{
				{Role: "system", Content: "You are a coding agent."},
				{Role: "user", Content: content},
			},
		}
	}

	sessionFor := func(content string) string {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		info := newRelayInfo("https://opencode.ai/inference", types.RelayFormatOpenAI)
		adaptor := &Adaptor{}
		_, err := adaptor.ConvertOpenAIRequest(c, info, buildRequest(content))
		require.NoError(t, err)
		header := http.Header{}
		require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
		session := header.Get("x-opencode-session")
		require.NotEmpty(t, session)
		return session
	}

	first := sessionFor("first conversation")
	assert.Equal(t, first, sessionFor("first conversation"))
	assert.NotEqual(t, first, sessionFor("another conversation"))
	assert.Regexp(t, `^ses_[0-9a-f]{32}$`, first)
}

func TestEndpointRootRejectsInvalidBaseURL(t *testing.T) {
	for _, baseURL := range []string{"ftp://opencode.ai", "opencode.ai/inference", "https://"} {
		_, _, err := endpointRoot(baseURL)
		assert.Error(t, err, baseURL)
	}
}
