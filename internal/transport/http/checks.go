package http

import (
	"context"
	"errors"
	"net/http"

	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

type PolicyReview interface {
	Run(context.Context, ports.Target) (checks.Execution, error)
}

func checkTarget(ctx context.Context, product, asset, revision string) (ports.Target, error) {
	principal, correlationID, err := caller(ctx)
	return ports.Target{
		Caller: domain.Principal(principal), ProductID: product, AssetID: asset, RevisionID: revision, CorrelationID: correlationID,
	}, err
}

func (o operations) RunRevisionCheck(ctx context.Context, request RunRevisionCheckRequestObject) (RunRevisionCheckResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	result, err := o.policyReview.Run(ctx, target)
	if err != nil {
		return nil, err
	}
	body := showCheck(result.Run)
	if result.Created {
		return RunRevisionCheck201JSONResponse{Body: body, Headers: RunRevisionCheck201ResponseHeaders{CacheControl: noStore}}, nil
	}
	return RunRevisionCheck200JSONResponse{Body: body, Headers: RunRevisionCheck200ResponseHeaders{CacheControl: noStore}}, nil
}

func showCheck(run domain.CheckRun) CheckRun {
	outcomes := make([]CheckRuleOutcome, 0, len(run.Outcomes))
	for _, outcome := range run.Outcomes {
		rule := outcome.Rule
		item := CheckRuleOutcome{
			RuleID: rule.ID, Description: rule.Description, Category: string(rule.Category), Severity: CheckRuleSeverity(rule.Severity),
			Waivable: rule.Waivable, Status: CheckRuleStatus(outcome.Status), FindingCount: outcome.Findings,
		}
		if outcome.Tool.Name != "" {
			item.Tool = &CheckRunTool{Name: outcome.Tool.Name, Version: outcome.Tool.Version}
		}
		outcomes = append(outcomes, item)
	}
	findings := make([]CheckRunFinding, 0, len(run.Findings))
	for _, finding := range run.Findings {
		item := CheckRunFinding{
			RuleID: finding.RuleID, Level: CheckRuleSeverity(finding.Level), Path: finding.Path, Message: finding.Message,
		}
		if finding.Pointer != "" {
			item.Pointer = &finding.Pointer
		}
		if finding.Line > 0 {
			item.Line = &finding.Line
		}
		findings = append(findings, item)
	}
	return CheckRun{CheckRun: showCheckOverview(run.Overview()), Outcomes: outcomes, Findings: findings}
}

func showCheckOverview(run domain.Overview) CheckRunOverview {
	tools := make([]CheckRunTool, 0, len(run.Tools))
	for _, tool := range run.Tools {
		tools = append(tools, CheckRunTool{Name: tool.Name, Version: tool.Version})
	}
	summary := run.Summary
	return CheckRunOverview{
		CheckRunID: run.ID, PolicySnapshotID: run.PolicySnapshotID, ProductID: run.Revision.ProductID,
		APIAssetID: run.Revision.AssetID, RevisionID: run.Revision.RevisionID, InputDigest: run.InputDigest,
		PolicySetID: run.PolicySetID, PolicyDigest: run.PolicyDigest, ToolManifest: tools, Status: CheckRunStatus(run.Status),
		Summary: CheckRunSummary{
			Pass: summary.Pass, Warn: summary.Warn, Fail: summary.Fail, Unknown: summary.Unknown,
			NotRun: summary.NotRun, FindingCount: summary.Findings,
		},
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
	}
}

func checkProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, checks.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller is not a current internal author of the workspace."), true
	case errors.Is(err, checks.ErrProductSuspended):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	default:
		return Problem{}, false
	}
}
