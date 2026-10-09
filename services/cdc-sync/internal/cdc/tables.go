// Package cdc is the CDC sync daemon: it polls every configured Postgres table
// by its (updated_at, id) cursor and upserts the changed rows into StarRocks
// with HTTP Stream Load.
package cdc

import (
	"fmt"
	"regexp"
	"strings"
)

// identifierPattern restricts configured database and table names to plain
// SQL identifiers. They end up in StarRocks DDL and Stream Load URLs, so
// anything fancier is refused up front rather than escaped everywhere.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// TableRef names one source table: the Postgres database it lives in and the
// table name, which is also the name of its StarRocks target table.
type TableRef struct {
	Database string
	Table    string
}

// String returns the "<database>.<table>" form used in config, logs, metric
// labels and the /status endpoint.
func (t TableRef) String() string {
	return t.Database + "." + t.Table
}

// ParseTables parses a comma-separated list of "<database>.<table>" entries.
// Target tables share one StarRocks database, so two sources with the same
// table name are rejected.
func ParseTables(list string) ([]TableRef, error) {
	var refs []TableRef
	targets := map[string]string{}

	for entry := range strings.SplitSeq(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		ref, err := parseTableRef(entry)
		if err != nil {
			return nil, err
		}
		if other, taken := targets[ref.Table]; taken {
			return nil, fmt.Errorf("tables %s and %s would both sync into StarRocks table %q", other, entry, ref.Table)
		}
		targets[ref.Table] = entry
		refs = append(refs, ref)
	}

	if len(refs) == 0 {
		return nil, fmt.Errorf("no tables configured (want <database>.<table>, ...)")
	}
	return refs, nil
}

func parseTableRef(entry string) (TableRef, error) {
	database, table, ok := strings.Cut(entry, ".")
	if !ok || !identifierPattern.MatchString(database) || !identifierPattern.MatchString(table) {
		return TableRef{}, fmt.Errorf("invalid table %q (want <database>.<table>, plain identifiers)", entry)
	}
	return TableRef{Database: database, Table: table}, nil
}
