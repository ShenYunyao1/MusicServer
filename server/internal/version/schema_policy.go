package version

// KnownSchema is the finite set understood by this bridge updater. Supporting
// a future schema requires code and tests, not merely a declaration in JSON.
func KnownSchema(schema int) bool { return schema == 7 || schema == 8 }

// CanMigrateSchema is a directed, explicit compatibility policy. The current
// server stays at schema 7 until the schema 8 tag definitions are finalized.
func CanMigrateSchema(from, to int) bool { return from == 7 && to == 8 }

// MigrationSources becomes part of the signed release identity when schema 8
// is activated. Legacy schema 7 manifests keep their existing representation.
func MigrationSources(target int) []int {
	if target == 8 {
		return []int{7}
	}
	return nil
}
