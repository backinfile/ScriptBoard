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
// 55 adds opt-in custom dashboard tabs without publishing existing panels. A
// parallel feature line used schemas 55-56 for custom browser tabs and role
// visibility; schema 57 reconciles both histories into one upgrade path;
// schema 58 adds per-function External Interface approval queues; schema 59
// distinguishes file and directory Quick access targets; schema 60 adds MCP
// OAuth clients, grants, opaque-token families and bounded invocation records;
// schema 61 shares the Quick Run grouping baseline with Schedules, Variables,
// file Quick access, and website monitoring; schema 62 adds an opt-in
// manual-start confirmation for Quick Runs; schema 63 records the visible
// source name for scheduled MySQL backups; schema 64 moves Redis logical
// database selection from connection metadata to individual read operations;
// schema 65 adds the grouped document collection.
//
// The explicit current-version guard forces a deliberate policy update when a
// future schema is introduced instead of silently promising an untested path.
func Compatible(current, existing int) bool {
	if existing == current {
		return true
	}
	switch current {
	case 57, 58, 59, 60, 61, 62, 63, 64, 65:
		return existing >= 20 && existing < current
	default:
		return false
	}
}
