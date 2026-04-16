package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type opsDetailRepoStub struct {
	opsRepoMock
	detail *OpsErrorLogDetail
}

func (s *opsDetailRepoStub) GetErrorLogByID(ctx context.Context, id int64) (*OpsErrorLogDetail, error) {
	if s.detail != nil {
		return s.detail, nil
	}
	return &OpsErrorLogDetail{}, nil
}

func TestOpsServiceGetErrorLogByID_PromotesLatestUpstreamRequestBody(t *testing.T) {
	repo := &opsDetailRepoStub{
		detail: &OpsErrorLogDetail{
			UpstreamErrors: `[
				{"message":"first","upstream_request_body":"{\"model\":\"gpt-4o\"}"},
				{"message":"second","upstream_request_body":"{\"model\":\"gpt-5.4\",\"instructions\":\"templated\"}"}
			]`,
		},
	}
	svc := NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	detail, err := svc.GetErrorLogByID(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, `{"model":"gpt-5.4","instructions":"templated"}`, detail.UpstreamRequestBody)
}

func TestExtractLatestUpstreamRequestBody_IgnoresEmptyPayloads(t *testing.T) {
	require.Empty(t, extractLatestUpstreamRequestBody(""))
	require.Empty(t, extractLatestUpstreamRequestBody("[]"))
	require.Empty(t, extractLatestUpstreamRequestBody("null"))
	require.Empty(t, extractLatestUpstreamRequestBody(`[{"message":"x"}]`))
}
