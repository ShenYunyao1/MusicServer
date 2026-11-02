package version

import "testing"

func TestListeningSchemaPolicy(t *testing.T) {
	if !KnownSchema(9) || !CanMigrateSchema(8, 9) || CanMigrateSchema(9, 8) || CanMigrateSchema(7, 9) {
		t.Fatal("schema 8 -> 9 must be explicit and directed")
	}
	sources := MigrationSources(9)
	if len(sources) != 1 || sources[0] != 8 {
		t.Fatal(sources)
	}
}
