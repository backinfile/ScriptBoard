// Package migrations owns the application schema compatibility and migration
// policy independently from the SQLite connection lifecycle.
package migrations

// Compatible reports whether an existing ScriptBoard schema can be migrated
// to current. Version 20 is the clean host-filesystem baseline. Versions 21-34
// introduced External Interfaces, Quick Access, MySQL, website
// monitoring, and custom dashboards. Two development lines then independently
// used 35-43; schema 44 reconciled them, schema 45 added the durable Registry
// operation log, schema 46 added its crash-safe completion phase, schema 47
// separated External Interface display labels from URL call names, and schema
// 48 made Kubernetes connections and retained history connection-scoped;
// schema 49 added intrinsic Variable value types; schema 50 added Variable
// revisions for visible modification tracking; schema 51 added optional notes
// to Variables; schema 52 added ScriptBoard node observation connections and
// read-only access tokens; schema 53 removed the obsolete privileged-account
// MFA enrollment deadline; schema 54 adds managed Redis connections; schema
// 55 adds opt-in custom dashboard tabs without publishing existing panels.
//
// The explicit current-version guard forces a deliberate policy update when a
// future schema is introduced instead of silently promising an untested path.
func Compatible(current, existing int) bool {
	return existing == current || current == 55 && existing >= 20 && existing <= 54
}
