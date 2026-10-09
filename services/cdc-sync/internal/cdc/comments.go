package cdc

import (
	"fmt"
	"strings"
)

// schemaChangeInProgress is how StarRocks refuses an ALTER while an earlier
// schema change job on the same table is still running. Loads keep working
// meanwhile, so it only means "try again later".
const schemaChangeInProgress = "schema change operation is in progress"

// CommentChanges is the DDL that brings a StarRocks table's comments in line
// with its Postgres source.
type CommentChanges struct {
	// Statements is empty when every comment already matches. The table
	// comment (an instant ALTER) comes first, then one MODIFY COLUMN ALTER
	// for every changed column.
	Statements []string
	// KeyCommentDiffers reports that id's comment differs. It is left as is:
	// MODIFY COLUMN on the key would cost a full schema change job for a
	// comment nobody needs to read to understand the key.
	KeyCommentDiffers bool
}

// commentChanges compares the desired comments with what an existing target
// holds. Columns the target lacks are skipped: ADD COLUMN gives them their
// comment. A missing target needs nothing here, since CREATE TABLE carries
// every comment.
func commentChanges(database, table string, desired TableSchema, current targetTable) CommentChanges {
	var changes CommentChanges
	if !current.exists() {
		return changes
	}

	if desired.Comment != current.Comment {
		changes.Statements = append(changes.Statements, tableCommentSQL(database, table, desired.Comment))
	}

	var changed []Column
	for _, c := range desired.Columns {
		comment, ok := current.Columns[strings.ToLower(c.Name)]
		if !ok || comment == c.Comment {
			continue
		}
		if c.Name == columnID {
			changes.KeyCommentDiffers = true
			continue
		}
		changed = append(changed, c)
	}
	if len(changed) > 0 {
		changes.Statements = append(changes.Statements, modifyColumnCommentsSQL(database, table, changed))
	}
	return changes
}

func tableCommentSQL(database, table, comment string) string {
	return fmt.Sprintf("ALTER TABLE %s.%s COMMENT = %s", quoteIdent(database), quoteIdent(table), quoteString(comment))
}

// modifyColumnCommentsSQL changes every column comment in one statement,
// because each ALTER of a column starts a schema change job and StarRocks
// refuses a second job while the first runs. The type is the one the column
// was created with, so only the comment changes. COMMENT is always given,
// even when empty, so a comment removed in Postgres is cleared here too.
func modifyColumnCommentsSQL(database, table string, columns []Column) string {
	clauses := make([]string, 0, len(columns))
	for _, c := range columns {
		clauses = append(clauses, "MODIFY COLUMN "+columnTypeDefinition(c)+" COMMENT "+quoteString(c.Comment))
	}
	return fmt.Sprintf("ALTER TABLE %s.%s %s", quoteIdent(database), quoteIdent(table), strings.Join(clauses, ", "))
}

// isSchemaChangeInProgress reports whether err is StarRocks refusing an
// ALTER because another schema change on the table hasn't finished.
func isSchemaChangeInProgress(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), schemaChangeInProgress)
}
