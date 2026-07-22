package go_partman

import "github.com/jirevwe/go_partman/internal/naming"

// TableName represents the components of a partition table name.
// Aliased from internal/naming so both the root package and
// internal/provisioner share the same Build/Parse grammar.
//
// The fully qualified form is:
//
//	{schema}.{parent}[_TENANT]_{YYYYMMDD|default}
type TableName = naming.TableName
