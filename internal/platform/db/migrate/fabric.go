package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
)

var (
	// contextDirectory is the name of a directory of migrations. It is the
	// package name of a bounded context and the name of its schema.
	contextDirectory = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

	// migrationFile is the name of a migration: a three digit number that
	// orders it inside its context, and what it does.
	migrationFile = regexp.MustCompile(`^(([0-9]{3})_[a-z0-9_]+)\.sql$`)
)

// reservedSchemas are the schemas no bounded context may claim: the two this
// package owns, the prefix of the framework's step IDs and the default schema
// of PostgreSQL. The schemas PostgreSQL reserves for itself carry an
// underscore, which a context directory cannot.
var reservedSchemas = []string{SchemaBento, SchemaHistory, "framework", "public"}

// fabricSteps reads the migrations Fabric owns from tree, laid out as
// <context>/<NNN>_<name>.sql. The steps come in the order of the context
// names and, inside a context, of the file numbers.
//
// A step is identified by its path without the extension, is applied to the
// schema named after its context and is fingerprinted with the SHA-256 of its
// bytes. Anything in the tree that is not such a file is an error rather than
// something to skip: a migration that is silently not part of the plan is a
// table a process expects and does not find.
func fabricSteps(tree fs.FS) ([]Step, error) {
	contexts, err := fs.ReadDir(tree, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read the Fabric migrations: %w", err)
	}

	var steps []Step
	for _, context := range contexts {
		name := context.Name()
		switch {
		case !context.IsDir():
			return nil, fmt.Errorf("migrate: %s is not the directory of a bounded context", name)
		case !contextDirectory.MatchString(name):
			return nil, fmt.Errorf("migrate: %q cannot name a schema; a context directory is lower case letters and digits", name)
		case slices.Contains(reservedSchemas, name):
			return nil, fmt.Errorf("migrate: no bounded context may claim the schema %s", name)
		}

		files, err := fs.ReadDir(tree, name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read the migrations of %s: %w", name, err)
		}
		numbers := make(map[string]string)
		for _, file := range files {
			path := name + "/" + file.Name()
			match := migrationFile.FindStringSubmatch(file.Name())
			if file.IsDir() || match == nil {
				return nil, fmt.Errorf("migrate: %s is not named <NNN>_<name>.sql", path)
			}
			stem, number := match[1], match[2]
			if other, taken := numbers[number]; taken {
				return nil, fmt.Errorf("migrate: %s and %s/%s carry the same number", path, name, other)
			}
			numbers[number] = file.Name()

			content, err := fs.ReadFile(tree, path)
			if err != nil {
				return nil, fmt.Errorf("migrate: read %s: %w", path, err)
			}
			if strings.TrimSpace(string(content)) == "" {
				return nil, fmt.Errorf("migrate: %s is empty", path)
			}
			digest := sha256.Sum256(content)
			steps = append(steps, Step{
				ID:       name + "/" + stem,
				Origin:   OriginFabric,
				Schema:   name,
				Checksum: "sha256:" + hex.EncodeToString(digest[:]),
				SQL:      string(content),
			})
		}
	}
	return steps, nil
}
