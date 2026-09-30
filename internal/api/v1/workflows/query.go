package workflows

import (
	"context"
	"fmt"
	"strings"

	"github.com/marmotdata/marmot/internal/core/assetrule"
	"github.com/marmotdata/marmot/internal/core/enrichment"
	"github.com/marmotdata/marmot/internal/core/workflow"
)

// queryMatcher starts runs for every asset an asset-rule query_expression matches.
type queryMatcher struct {
	rules assetrule.Service
}

// NewQueryMatcher adapts asset-rule preview to workflow.QueryMatcher.
func NewQueryMatcher(rules assetrule.Service) workflow.QueryMatcher {
	return &queryMatcher{rules: rules}
}

func (m *queryMatcher) Match(ctx context.Context, queryExpression string, limit int) ([]string, int, error) {
	q := strings.TrimSpace(queryExpression)
	preview, err := m.rules.PreviewRule(ctx, assetrule.RulePreviewInput{
		RuleType:        enrichment.RuleTypeQuery,
		QueryExpression: &q,
	}, limit)
	if err != nil {
		return nil, 0, err
	}
	if len(preview.Errors) > 0 {
		return nil, 0, fmt.Errorf("%s", strings.Join(preview.Errors, "; "))
	}
	return preview.AssetIDs, preview.AssetCount, nil
}
