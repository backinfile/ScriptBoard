// Package web owns ScriptBoard's HTTP interface, route catalog, middleware,
// server-rendered views, and Web process lifecycle. Domain behavior belongs in
// the internal modules it composes; privileged credentials and host mutations
// remain behind Broker adapters.
package web
