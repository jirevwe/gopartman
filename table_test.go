package go_partman

import (
	"fmt"
	"testing"
	"time"
)

type TestTable struct {
	name          string
	tableName     TableName
	formattedName string
	wantError     bool
	err           error
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
		expected  TableName
		wantError bool
	}{
		{
			name:  "table with default partition",
			input: "test.user_logs_default",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				IsDefault:  true,
			},
		},
		{
			name:  "table with date",
			input: "test.user_logs_20240101",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				Bounds: Bounds{
					From: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		},
		{
			name:  "table with tenant id",
			input: "test.user_logs_TENANT1_20240101",
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
			name:  "table with tenant id and default partition",
			input: "test.user_logs_TENANT2_default",
			expected: TableName{
				SchemaName: "test",
				ParentName: "user_logs",
				TenantId:   "TENANT2",
				IsDefault:  true,
			},
		},
		// Error cases parallel with TestTableNameGen: hyphens and spaces
		// cannot appear in a valid Build output; Parse rejects them.
		{
			name:      "invalid parent table name - hyphen",
			input:     "test.user-logs_default",
			wantError: true,
		},
		{
			name:      "invalid tenant id - hyphen",
			input:     "test.user_logs_TENANT-2_default",
			wantError: true,
		},
		{
			name:      "invalid tenant id - space",
			input:     "test.user_logs_TENANT 2_default",
			wantError: true,
		},
		{
			name:      "missing schema separator",
			input:     "user_logs_default",
			wantError: true,
		},
		{
			name:      "unknown suffix",
			input:     "test.user_logs_something",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TableName{}.Parse(tt.input)
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built, err := tc.in.Build()
			if err != nil {
				t.Fatalf("Build failed: %v", err)
			}
			parsed, err := TableName{}.Parse(built)
			if err != nil {
				t.Fatalf("Parse failed: %v", err)
			}
			if parsed != tc.in {
				t.Errorf("round-trip mismatch\ngot  = %#v\nwant = %#v", parsed, tc.in)
			}
		})
	}
}
