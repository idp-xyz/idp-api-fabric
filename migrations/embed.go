// Package migrations holds the SQL of the tables Fabric owns, one directory
// per bounded context (F07 §3). A bounded context is the only owner of its
// tables and keeps them in a schema of its own name. No file here touches the
// schema of another context or of the framework, and no table refers to a
// table of another context: contexts share identifiers, not rows (F03 §3.2).
//
// A file is named <NNN>_<name>.sql. fabric-migrate applies it once, in a
// transaction that also records it. A file that has been applied is never
// edited: its checksum is recorded, and a database that holds another one is
// refused. A change is a new file (F07 §5.3).
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed */*.sql
var files embed.FS

// Files is the tree of migrations: <context>/<NNN>_<name>.sql.
func Files() fs.FS { return files }
