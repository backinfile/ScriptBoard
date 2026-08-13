// Package migrations owns the application schema compatibility and migration
// policy independently from the SQLite connection lifecycle.
package migrations

// Compatible reports whether an existing ScriptBoard schema can be migrated
// to current. Version 20 is the clean host-filesystem baseline. Versions 21-34
// introduced Assistant, External Interfaces, Quick Access, MySQL, website
// monitoring, and custom dashboards. Two development lines then independently
// used 35-43; schema 44 reconciled them, and schema 45 added the durable
// Registry operation log.
//
// The explicit current-version guard forces a deliberate policy update when a
// future schema is introduced instead of silently promising an untested path.
func Compatible(current, existing int) bool {
	return existing == current || current == 45 && existing >= 20 && existing <= 44
}
