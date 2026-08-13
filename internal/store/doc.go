// Package store owns ScriptBoard's durable SQLite lifecycle and migration
// primitives. Product domains describe their schema; store enforces how that
// schema is inspected, opened, committed, and checkpointed.
package store
