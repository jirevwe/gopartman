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
