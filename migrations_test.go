package go_partman

import "testing"

func TestMigrationsSanity(t *testing.T) {
	ms := Migrations()
	if got, want := len(ms), 6; got != want {
		t.Fatalf("Migrations() len = %d, want %d", got, want)
	}
	for i, m := range ms {
		wantVersion := i + 1
		if m.Version != wantVersion {
			t.Errorf("Migrations()[%d].Version = %d, want %d", i, m.Version, wantVersion)
		}
		if m.Name == "" {
			t.Errorf("Migrations()[%d].Name empty", i)
		}
		if len(m.SQL) == 0 {
			t.Errorf("Migrations()[%d].SQL empty", i)
		}
	}
}
