package gopartman

import (
	"fmt"
	"testing"
	"time"
)

type TestTable struct {
	err           error
	name          string
	formattedName string
	tableName     TableName
	wantError     bool
}

func TestTableNameGen(t *testing.T) {
	tests := []TestTable{
		{
			name: "table with default partition",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			formattedName: "test.user_logs_default",
		},
		{
			name: "table with date",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			formattedName: "test.user_logs_20240101",
		},
		{
			name: "table with tenant id",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT1",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
			},
			formattedName: "test.user_logs_TENANT1_20240101",
		},
		{
			name: "table with lowercased tenant id",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "tenant2",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			formattedName: "test.user_logs_TENANT2_20240101", // daily
		},
		{
			name: "table with tenant id and default partition",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT2",
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			formattedName: "test.user_logs_TENANT2_default",
		},
		{
			name: "table with lowercase tenant id and default partition",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "tenant2",
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			formattedName: "test.user_logs_TENANT2_default",
		},
		{
			name: "invalid parent table name - table name with hyphen",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user-logs",
				TenantId:   "tenant2",
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			wantError: true,
			err:       fmt.Errorf("parent table name cannot contain hyphens"),
		},
		{
			name: "invalid partition name - tenant_id with hyphen",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "tenant-2", // should not have a hyphen
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			wantError: true,
			err:       fmt.Errorf("tenant id cannot contain hyphens"),
		},
		{
			name: "invalid tenant id",
			tableName: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "tenant 2", // should not have a space
				IsDefault:  true,
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
					To:   time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC),
				},
			},
			wantError: true,
			err:       fmt.Errorf("tenant id cannot contain spaces"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formattedName, err := tt.tableName.Build()

			if err != nil && !tt.wantError {
				t.Errorf("got error: %v, but want none", err)
			} else if err == nil && tt.wantError {
				t.Errorf("want error: %v but got none", tt.err)
			}

			if tt.formattedName != formattedName {
				t.Errorf("got = %v, want %v", formattedName, tt.formattedName)
			}
		})
	}
}

// TestTableNameParse covers the same case matrix as TestTableNameGen.
// Parse leaves Bounds.To and Interval as zero; test cases match.
func TestTableNameParse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		parent    string
		expected  TableName
		wantError bool
	}{
		{
			name:   "table with default partition",
			input:  "test.user_logs_default",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				IsDefault:  true,
			},
		},
		{
			// The regression guard for tenant detection: "logs" is the last
			// word of the parent, not a tenant, and only the parent name
			// distinguishes the two.
			name:   "table with date",
			input:  "test.user_logs_20240101",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name:   "table with tenant id",
			input:  "test.user_logs_TENANT1_20240101",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT1",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name:   "table with tenant id and default partition",
			input:  "test.user_logs_TENANT2_default",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT2",
				IsDefault:  true,
			},
		},
		{
			// PostgreSQL folds unquoted identifiers, so a partition created
			// by DDL outside this package carries a lower-case tenant. It is
			// still that tenant's partition and must be adoptable.
			name:   "folded tenant id is read as written",
			input:  "test.user_logs_tenant1_20240101",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "tenant1",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name:   "folded parent segment still matches",
			input:  "test.USER_LOGS_TENANT1_20240101",
			parent: "user_logs",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT1",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		// Error cases parallel with TestTableNameGen: hyphens and spaces
		// cannot appear in a valid Build output; Parse rejects them.
		{
			name:      "invalid parent table name - hyphen",
			input:     "test.user-logs_default",
			parent:    "user-logs",
			wantError: true,
		},
		{
			name:      "invalid tenant id - hyphen",
			input:     "test.user_logs_TENANT-2_default",
			parent:    "user_logs",
			wantError: true,
		},
		{
			name:      "invalid tenant id - space",
			input:     "test.user_logs_TENANT 2_default",
			parent:    "user_logs",
			wantError: true,
		},
		{
			name:      "missing schema separator",
			input:     "user_logs_default",
			parent:    "user_logs",
			wantError: true,
		},
		{
			name:      "unknown suffix",
			input:     "test.user_logs_something",
			parent:    "user_logs",
			wantError: true,
		},
		{
			name:      "name belongs to a different parent",
			input:     "test.audit_logs_TENANT1_20240101",
			parent:    "user_logs",
			wantError: true,
		},
		{
			name:      "empty tenant segment",
			input:     "test.user_logs__20240101",
			parent:    "user_logs",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TableName{}.Parse(tt.input, tt.parent)
			if tt.wantError {
				if err == nil {
					t.Errorf("want error but got none: got=%#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("got = %#v, want = %#v", got, tt.expected)
			}
		})
	}
}

// TestTableNameRoundTrip proves Parse(Build(x)) == x for every case
// (as required by ADR-0001 acceptance).
func TestTableNameRoundTrip(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		in   TableName
	}{
		{
			name: "default partition",
			in: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				IsDefault:  true,
			},
		},
		{
			name: "bounded partition",
			in: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				Bounds:     Bounds{From: from},
			},
		},
		{
			name: "tenant + bounded",
			in: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT1",
				Bounds:     Bounds{From: from},
			},
		},
		{
			name: "tenant + default",
			in: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT2",
				IsDefault:  true,
			},
		},
		{
			// Build validates the tenant with ^\w+$, so an underscore is a
			// legal tenant. Recovering it needs the parent name: no property
			// of "user_logs_TENANT_2_20240101" says where the parent ends.
			name: "tenant containing underscore",
			in: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT_2",
				Bounds:     Bounds{From: from},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built, err := tc.in.Build()
			if err != nil {
				t.Fatalf("Build failed: %v", err)
			}
			parsed, err := TableName{}.Parse(built, tc.in.ParentName)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if parsed != tc.in {
				t.Errorf("round-trip mismatch\ngot  = %#v\nwant = %#v", parsed, tc.in)
			}
		})
	}
}
