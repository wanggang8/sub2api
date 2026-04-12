package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldApplyForcedCodexInstructionsForRequest(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name  string
		path  string
		model string
		want  bool
	}{
		{name: "cursor responses gpt5", path: "/cursor/v1/responses", model: "gpt-5.4", want: true},
		{name: "cursor chat completions gpt5", path: "/cursor/v1/chat/completions", model: "gpt-5.2", want: true},
		{name: "cursor anthropic compat gpt5", path: "/cursor/v1/messages", model: "gpt-5.1", want: true},
		{name: "cursor path non gpt5", path: "/cursor/v1/responses", model: "gpt-4o", want: false},
		{name: "non cursor path gpt5", path: "/v1/responses", model: "gpt-5.4", want: false},
		{name: "empty model", path: "/cursor/v1/responses", model: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)

			require.Equal(t, tt.want, shouldApplyForcedCodexInstructionsForRequest(c, tt.model))
		})
	}
}

