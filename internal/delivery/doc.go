// Package delivery derives where each work order's time went from its ledger events, with no model and no
// clock (ADR-0026). The derivation is a pure function with a version, so a new version re-derives every
// timeline from the events rather than migrating what an earlier one stored.
package delivery
