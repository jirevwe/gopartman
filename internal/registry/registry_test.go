package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDropper struct {
	calls []ParentRef
	err   error
}

func (f *fakeDropper) DropAll(_ context.Context, parent ParentRef) error {
	f.calls = append(f.calls, parent)
	return f.err
}

func TestEvalRemoveOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []RemoveOption
		want bool
	}{
		{"no opts", nil, false},
		{"cascade", []RemoveOption{WithCascadeDrop()}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalRemoveOptions(tc.opts).cascadeDrop; got != tc.want {
				t.Errorf("cascadeDrop = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty", "", true},
		{"hyphen", "foo-bar", true},
		{"space", "foo bar", true},
		{"quote", "foo\"bar", true},
		{"valid_snake", "foo_bar", false},
		{"valid_alnum", "foo123", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateIdentifier("field", tc.value)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateIdentifier(%q) err=%v, wantErr=%v", tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestValidateParentIdentifiers_RequiredFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  ParentConfig
	}{
		{"missing schema", ParentConfig{TableName: "t", PartitionBy: "c"}},
		{"missing table", ParentConfig{SchemaName: "s", PartitionBy: "c"}},
		{"missing partition_by", ParentConfig{SchemaName: "s", TableName: "t"}},
		{"invalid tenant column", ParentConfig{SchemaName: "s", TableName: "t", PartitionBy: "c", TenantColumn: "bad-name"}},
		{"invalid retention schema", ParentConfig{SchemaName: "s", TableName: "t", PartitionBy: "c", RetentionSchema: "bad name"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateParentIdentifiers(tc.cfg); err == nil {
				t.Errorf("expected error, got nil")
			}
		})
	}
}

func TestRemoveParent_CascadeWithoutDropperReturnsError(t *testing.T) {
	// Build an Impl with no pool and no dropper. The cascade branch
	// short-circuits before any DB call, so a nil pool is fine.
	r := &Impl{}
	err := r.RemoveParent(context.Background(), ParentRef{SchemaName: "s", TableName: "t"}, WithCascadeDrop())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "WithCascadeDrop requires Retention") {
		t.Errorf("error message does not mention retention: %v", err)
	}
}

func TestRemoveParent_CascadeCallsDropperBeforeMetadataDelete(t *testing.T) {
	// This test asserts ordering only. It uses a dropper that returns
	// an error so we can prove RemoveParent aborts BEFORE the metadata
	// delete without needing a live pool.
	dropErr := errors.New("boom")
	dropper := &fakeDropper{err: dropErr}
	r := &Impl{dropper: dropper}
	err := r.RemoveParent(context.Background(), ParentRef{SchemaName: "s", TableName: "t"}, WithCascadeDrop())
	if err == nil || !errors.Is(err, dropErr) {
		t.Fatalf("expected wrapped dropErr, got %v", err)
	}
	if len(dropper.calls) != 1 {
		t.Errorf("dropper called %d times, want 1", len(dropper.calls))
	}
}

func TestNew_RequiredFields(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("expected error when Pool missing")
	}
}
