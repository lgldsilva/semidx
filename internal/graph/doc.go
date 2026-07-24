// Package graph provides dependency-graph primitives over the indexed
// file_dependencies adjacency map: ego subgraphs and shortest paths between
// files, expanding through package-directory targets the way the indexer stores
// them (source file → imported package dir with a trailing slash).
//
// See docs/design-decisions.md § "File↔package dependency graph contract".
package graph
