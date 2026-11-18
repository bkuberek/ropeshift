// Package practicerun runs the practices check over one checkout: it reads the repository through the practices
// ports and has the practices domain decide each practice (FR-PRAC-1).
package practicerun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
)

// Sources are what a run reads through.
type Sources struct {
	Snapshots ports.PracticeSnapshots
	Secrets   ports.SecretScans
	Forge     ports.ForgeSettings
	Clock     ports.Clock
}

// Request says which checkout to check and what the check knows of its repository.
type Request struct {
	// Repository and Worker name the repository and the worker the result is for. Worker is empty when unknown.
	Repository string
	Worker     string
	// Dir is the checkout's directory.
	Dir string
	// BaseBranch is the branch the check reads, and the branch the forge's settings are read for.
	BaseBranch string
	// Binding is the repository's forge binding, the zero value for none.
	Binding ledger.ForgeBinding
	// MergeAuthority is the repository's merge authority; empty counts as the default.
	MergeAuthority ledger.MergeAuthority
	// ScannedFrom is the commit the last secret scan read, empty to scan the whole history.
	ScannedFrom string
}

// Run checks one checkout. It reads the snapshot of the base branch once and scans for secrets with the commit that
// snapshot names, so the files and the scan are of one commit. A snapshot that cannot be read, or a context that
// ends, fails the run. A scan that fails leaves the `secrets` practice unknown with the reason, and a settings read
// that fails leaves the forge practices unknown with it.
//
// A settings read the forge refused for a rate limit returns the result its other facts give, and an error that
// wraps ports.ErrForgeRateLimited, so the caller records the result and holds its reads of the repository.
func Run(ctx context.Context, sources Sources, req Request) (practices.Result, error) {
	snapshot, err := sources.Snapshots.Snapshot(ctx, req.Dir, req.BaseBranch)
	if err != nil {
		return practices.Result{}, fmt.Errorf("read %s at %s: %w", req.Dir, req.BaseBranch, err)
	}
	in := practices.Input{
		Repository: req.Repository, Commit: snapshot.Commit, BaseBranch: req.BaseBranch, Worker: req.Worker,
		CheckedAt: sources.Clock.Now(), RulesVersion: ledger.CredentialRulesVersion,
		Paths: snapshot.Paths, Read: snapshot.Read, Ignored: snapshot.Ignored,
		MergeAuthority: string(req.MergeAuthority),
	}
	scan, err := sources.Secrets.Scan(ctx, req.Dir, snapshot.Commit, req.ScannedFrom)
	switch {
	case ctx.Err() != nil:
		return practices.Result{}, fmt.Errorf("scan %s for secrets: %w", req.Dir, ctx.Err())
	case err != nil:
		in.SecretsUnknown = reasonOf("the secret scan failed: ", err)
	default:
		in.Secrets, in.ScannedFrom, in.Skipped = scan.Matches, scan.ScannedFrom, scan.Skipped
	}
	var settingsErr error
	if req.Binding != (ledger.ForgeBinding{}) {
		facts, err := sources.Forge.Settings(ctx, ports.ForgeRepo{Dir: req.Dir, Binding: req.Binding}, req.BaseBranch)
		switch {
		case ctx.Err() != nil:
			return practices.Result{}, fmt.Errorf("read the settings of %s: %w", req.Binding, ctx.Err())
		case errors.Is(err, ports.ErrForgeRateLimited):
			facts = unsettledUnknown(facts, reasonOf("", err))
			settingsErr = fmt.Errorf("read the settings of %s: %w", req.Binding, err)
		case err != nil:
			why := reasonOf("the forge settings could not be read: ", err)
			facts = practices.ForgeFacts{VisibilityUnknown: why, ProtectedUnknown: why, ReviewUnknown: why}
		}
		in.Forge = &facts
	}
	result := practices.Evaluate(in)
	if ctx.Err() != nil {
		return practices.Result{}, fmt.Errorf("check %s: %w", req.Dir, ctx.Err())
	}
	return result, settingsErr
}

// unsettledUnknown leaves each fact a rate-limited read did not settle unknown with the reason. A fact the read
// settled has a value or no reason to be unknown, and a zero value with no reason is a fact nothing was read for.
func unsettledUnknown(facts practices.ForgeFacts, why string) practices.ForgeFacts {
	if facts.Visibility == "" && facts.VisibilityUnknown == "" {
		facts.VisibilityUnknown = why
	}
	if !facts.Protected && facts.ProtectedUnknown == "" {
		facts.ProtectedUnknown = why
	}
	if facts.ReviewCount <= 0 && facts.ReviewUnknown == "" {
		facts.ReviewUnknown = why
	}
	return facts
}

// MaxReason bounds, in bytes, the text a failed read puts in a result or a report.
const MaxReason = 2000

// Clip writes text on one line, cut at MaxReason bytes on a rune boundary.
func Clip(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > MaxReason {
		cut := MaxReason
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	return text
}

// reasonOf writes a prefix and an error's text on one line, cut at MaxReason bytes on a rune boundary.
func reasonOf(prefix string, err error) string {
	return Clip(prefix + err.Error())
}
