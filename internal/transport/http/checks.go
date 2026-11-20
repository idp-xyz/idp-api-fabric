package http

import (
	"context"
	"errors"
	"net/http"

	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type PolicyReview interface {
	Run(context.Context, ports.Target, string) (checks.Execution, error)
	Get(context.Context, ports.Target, string) (domain.CheckRun, error)
	List(context.Context, checks.ListChecks) (checks.Page, error)
	Compare(context.Context, ports.Target, string) (checks.RevisionComparison, error)
	ContractReviews
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
	var baselineID string
	if request.Params.BaselineRevisionID != nil {
		baselineID = *request.Params.BaselineRevisionID
	}
	result, err := o.policyReview.Run(ctx, target, baselineID)
	if err != nil {
		return nil, err
	}
	body := showCheck(result.Run)
	if result.Created {
		location := "/v1/products/" + target.ProductID + "/assets/" + target.AssetID + "/revisions/" + target.RevisionID + "/check-runs/" + result.Run.ID
		return RunRevisionCheck201JSONResponse{Body: body, Headers: RunRevisionCheck201ResponseHeaders{
			CacheControl: noStore, Location: location,
		}}, nil
	}
	return RunRevisionCheck200JSONResponse{Body: body, Headers: RunRevisionCheck200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GetRevisionCheck(ctx context.Context, request GetRevisionCheckRequestObject) (GetRevisionCheckResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	run, err := o.policyReview.Get(ctx, target, request.CheckRunID)
	if err != nil {
		return nil, err
	}
	return GetRevisionCheck200JSONResponse{
		Body: showCheck(run), Headers: GetRevisionCheck200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) ListRevisionChecks(ctx context.Context, request ListRevisionChecksRequestObject) (ListRevisionChecksResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	list := "check-runs/" + target.ProductID + "/" + target.AssetID + "/" + target.RevisionID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.policyReview.List(ctx, checks.ListChecks{Target: target, PageSize: size, After: after})
	if err != nil {
		return nil, err
	}
	items := make([]CheckRunOverview, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, showCheckOverview(item))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListRevisionChecks200JSONResponse{
		Body: CheckRunList{Items: items, NextPageToken: next}, Headers: ListRevisionChecks200ResponseHeaders{CacheControl: noStore},
	}, nil
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
	body := CheckRun{CheckRun: showCheckOverview(run.Overview()), Outcomes: outcomes, Findings: findings}
	if evidence := run.Compatibility; evidence != nil {
		comparison := showRevisionComparison(
			ComparedRevision{RevisionID: evidence.Baseline.RevisionID, InputDigest: evidence.Baseline.InputDigest},
			ComparedRevision{RevisionID: run.Revision.RevisionID, InputDigest: run.InputDigest},
			evidence.Tool, evidence.Comparison,
		)
		body.Compatibility = &comparison
	}
	return body
}

func showCheckOverview(run domain.Overview) CheckRunOverview {
	tools := make([]CheckRunTool, 0, len(run.Tools))
	for _, tool := range run.Tools {
		tools = append(tools, CheckRunTool{Name: tool.Name, Version: tool.Version})
	}
	summary := run.Summary
	item := CheckRunOverview{
		CheckRunID: run.ID, PolicySnapshotID: run.PolicySnapshotID, ProductID: run.Revision.ProductID,
		APIAssetID: run.Revision.AssetID, RevisionID: run.Revision.RevisionID, InputDigest: run.InputDigest,
		PolicySetID: run.PolicySetID, PolicyDigest: run.PolicyDigest, ToolManifest: tools, Status: CheckRunStatus(run.Status),
		Summary: CheckRunSummary{
			Pass: summary.Pass, Warn: summary.Warn, Fail: summary.Fail, Unknown: summary.Unknown,
			NotRun: summary.NotRun, FindingCount: summary.Findings,
		},
		StartedAt: run.StartedAt, CompletedAt: run.CompletedAt,
	}
	if evidence := run.Compatibility; evidence != nil {
		item.Compatibility = &CheckRunComparison{
			Baseline:       ComparedRevision{RevisionID: evidence.Baseline.RevisionID, InputDigest: evidence.Baseline.InputDigest},
			Classification: CompatibilityClassification(evidence.Classification),
			Tool:           CheckRunTool{Name: evidence.Tool.Name, Version: evidence.Tool.Version},
		}
	}
	return item
}

func checkProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, checks.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller holds no role in the workspace that allows this."), true
	case errors.Is(err, checks.ErrCheckRunNotFound):
		return problem(http.StatusNotFound, "CHECK_RUN_NOT_FOUND", ""), true
	case errors.Is(err, checks.ErrInvalidPage):
		return problem(http.StatusBadRequest, CodeRequestInvalid, ""), true
	case errors.Is(err, checks.ErrInvalidBaseline):
		return problem(http.StatusBadRequest, CodeRequestInvalid, "The baseline must be a different revision of the same API asset."), true
	case errors.Is(err, checks.ErrProductSuspended):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	default:
		return reviewProblem(err)
	}
}
