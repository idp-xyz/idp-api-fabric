package postgres

// ListWorkspaceStatementForTest is the statement that reads a page of the
// reviews of a workspace, kept to the status, for the test that looks at what
// a page costs.
func ListWorkspaceStatementForTest(status string) (string, error) {
	return listWorkspaceStatement(status)
}

// LatestOverviewsForTest and LatestOverviewsOfStatusForTest are the two
// statements that read the last check of each of a set of revisions, for the
// test that counts what they read.
const (
	LatestOverviewsForTest         = latestOverviews
	LatestOverviewsOfStatusForTest = latestOverviewsOfStatus
)
