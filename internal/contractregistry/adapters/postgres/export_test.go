package postgres

import (
	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

// SelectHeadsForTest is the statement that reads the heads of a set of assets,
// for the test that counts what it reads.
const SelectHeadsForTest = selectHeads

// ScanHeadForTest reads a row of that statement, for the test of a row the
// statement cannot make.
func ScanHeadForTest(row pgx.CollectableRow) (ports.Head, error) { return scanHead(row) }
