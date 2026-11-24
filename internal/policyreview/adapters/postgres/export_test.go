package postgres

// ListWorkspaceStatementForTest is the statement that reads a page of the
// reviews of a workspace, kept to the status, for the test that looks at what
// a page costs.
func ListWorkspaceStatementForTest(status string) (string, error) {
	return listWorkspaceStatement(status)
}
