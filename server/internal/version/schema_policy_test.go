package version

import "testing"

func TestSchemaPolicyBridgeIsFiniteAndDirected(t *testing.T) {
	for _, schema := range []int{-1, 0, 1, 6, 9, 100} {
		if KnownSchema(schema) || len(MigrationSources(schema)) != 0 {
			t.Fatalf("unsupported schema %d was accepted", schema)
		}
	}
	for _, pair := range [][2]int{{7, 7}, {8, 8}, {8, 7}, {7, 9}, {6, 8}, {0, 8}} {
		if CanMigrateSchema(pair[0], pair[1]) {
			t.Fatalf("unsupported migration %v was accepted", pair)
		}
	}
	if !KnownSchema(7) || !KnownSchema(8) || !CanMigrateSchema(7, 8) || len(MigrationSources(7)) != 0 {
		t.Fatal("bridge compatibility is incorrect")
	}
	sources := MigrationSources(8)
	if len(sources) != 1 || sources[0] != 7 {
		t.Fatal("schema 8 migration sources are incorrect", sources)
	}
	sources[0] = 100
	if MigrationSources(8)[0] != 7 {
		t.Fatal("caller mutated the schema policy")
	}
	if Schema != 7 {
		t.Fatal("activate schema 8 only after its tag DDL is finalized")
	}
}
