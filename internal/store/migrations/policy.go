// Package migrations owns the application schema compatibility and migration
// policy independently from the SQLite connection lifecycle.
package migrations

// Compatible reports whether an existing ScriptBoard schema can be migrated
// to current. Version 20 is the clean host-filesystem baseline. Versions 21-34
// introduced Assistant, External Interfaces, Quick Access, MySQL, website
// monitoring, and custom dashboards. Two development lines then independently
// used 35-43; schema 44 reconciled them, schema 45 added the durable Registry
// operation log, schema 46 added its crash-safe completion phase, and schema
// 47 made Kubernetes connections and retained history connection-scoped.
//
// The explicit current-version guard forces a deliberate policy update when a
// future schema is introduced instead of silently promising an untested path.
func Compatible(current, existing int) bool {
	return existing == current || current == 47 && existing >= 20 && existing <= 46
}
