package entry

import (
	"context"
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/adapters/ghcli"
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectremote"
)

// machineRuntime is what `ropeshift new` makes a folder through on this machine: its folders and git, and its `gh`
// under the person's own sign-in, the same ports a worker writes a member's template with.
func machineRuntime() projectremote.Runtime {
	return projectremote.Runtime{
		Folders: projectfolders.Folders{}, Forge: ghcli.Accounts{}, Render: renderProjectTemplate, Machine: "this machine",
	}
}

// newProject makes the folder a request names, as the worker does for the web UI: the checks of `gh` first, so a
// refusal writes and creates nothing, then the folders above the new one, then the files, each first commit, and each remote. It keeps no journal, because
// the person runs it in front of a terminal and runs it again after a failure.
func newProject(ctx context.Context, request ledger.TemplateWriteRequest, needsRepository bool) (ledger.TemplateWriteReport, error) {
	runtime := machineRuntime()
	job := projectremote.Job{Request: request, NeedsRepository: needsRepository}
	var progress projectremote.Progress
	refusal, err := projectremote.Start(ctx, runtime, job, &progress)
	if err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	if refusal != nil {
		return *refusal, nil
	}
	// The folders above the new one are made once the checks have passed, so a refusal leaves nothing on disk.
	if err := os.MkdirAll(filepath.Dir(request.Path), 0o755); err != nil {
		return ledger.TemplateWriteReport{}, err
	}
	return projectremote.Finish(ctx, runtime, job, &progress, nil)
}

// readForgeAccount reads this machine's `gh`: whether it can create repositories, and under which owners.
func readForgeAccount(ctx context.Context) (ledger.ForgeAccountReport, error) {
	return projectremote.ReadAccount(ctx, machineRuntime())
}
