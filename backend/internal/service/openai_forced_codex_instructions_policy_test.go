package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldApplyForcedCodexInstructionsForRequest(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		enabled bool
		model   string
		want    bool
	}{
		{name: "enabled gpt5", enabled: true, model: "gpt-5.4", want: true},
		{name: "enabled non gpt5", enabled: true, model: "gpt-4o", want: true},
		{name: "enabled custom model", enabled: true, model: "CustomModel", want: true},
		{name: "disabled gpt5", enabled: false, model: "gpt-5.4", want: false},
		{name: "enabled empty model", enabled: true, model: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			SetForcedCodexInstructionsEnabled(c, tt.enabled)

			require.Equal(t, tt.want, shouldApplyForcedCodexInstructionsForRequest(c, tt.model))
		})
	}
}
