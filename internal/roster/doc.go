// Package roster is the agent roster (ADR-0025): agent definitions by kind of work, with
// driver-neutral capability classes and tool categories, a routing guide for the session that
// coordinates them, and the default mapping that chooses the agent a lane runs as.
//
// The resolved roster is the built-in roster, then the installation's layer, then a project's,
// field by field, and every field names the layer it came from. Nothing here reads a repository:
// a plan or an agent can edit a repository, and the roster shapes what agents do.
package roster
