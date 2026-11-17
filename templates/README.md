# Project templates

These templates are the files a project starts from: the instructions agents read, the plan files, the `Makefile`, the ignore files, the policies, and the profiles. They are the one source of those files. Each is a Go [`text/template`](https://pkg.go.dev/text/template) over `templates.Data`, embedded in the binary, and the package's test reads each rendering with the product's own readers, so a template that no longer parses fails the build.

## The package

`templates.Names()` lists every template by its path without `.template`, sorted, such as `workspace/agents.md`, `workspace/plan/contract.md`, `user/profile.toml`, and `policies/team/decision-policy.toml`. `templates.Render(name, data)` returns one template's text, and an unknown name is `templates.ErrUnknown`.

`templates.Data` holds three fields:

| Field | Holds |
|-------|-------|
| `Project` | The project's name |
| `Repositories` | The project's code repositories, each with a `Name` and a `Remote` |
| `Example` | Whether to render the example |

With `Example` true, a template renders the example project: `acme`, with the repositories `acme-api` and `acme-web`, and the comments that explain each part. `Project` and `Repositories` are then ignored.

With `Example` false, a template renders the starting file for the project in `Project`, with one entry for each of `Repositories`, or none: a plan whose `documents` lists only the files a new project has, a goal and a requirements file with one placeholder each, the requirement `QA-1` that every verify criterion names, a `Makefile` whose `REPOSITORIES` lists the given repositories, and a `catalog-info.yaml` with one component.

## Every template ends in `.template`

Every file here except `embed.go`, its tests, and this file ends in `.template`. Intake reads every committed `.md` file whose first line is `+++` in a repository it reads, and an agent CLI loads an `AGENTS.md` or `CLAUDE.md` from the folder it works in, so a template under its real name would become a plan record or an agent's instructions in this repository. A project's file drops the suffix.

## The templates

| Template | Becomes | Use it when |
|----------|---------|-------------|
| [workspace/agents.md.template](workspace/agents.md.template) | `AGENTS.md` at a workspace's root | A project of several repositories: the project definition, where things live, which document wins, IDs, the rules for plan files, privacy, and the steps that need a person |
| [monorepo/agents.md.template](monorepo/agents.md.template) | `AGENTS.md` at a monorepo's root | A project of one repository, which is its own planning repository: one file for the project, the plan's rules, and the code's rules |
| [repository/agents.md.template](repository/agents.md.template) | `AGENTS.md` at a code repository's root | Each code repository: its commands, layout, rules, and the rules a lane follows there |
| [claude.md.template](claude.md.template) | `CLAUDE.md` beside every `AGENTS.md` | Always: it imports `AGENTS.md`, so Claude Code reads the file other agent CLIs read |
| [workspace/makefile.template](workspace/makefile.template) | `Makefile` at a workspace's root | A workspace: `setup`, `fetch`, `status`, `hooks`, `register-check`, `register`, and `lanes` |
| [workspace/gitignore.template](workspace/gitignore.template) | `.gitignore` at a workspace's root | A workspace: it ignores the folder of clones and `.env` files |
| [monorepo/gitignore.template](monorepo/gitignore.template) | `.gitignore` at a monorepo's root | A monorepo: it ignores `.env` files |
| [repository/gitignore.template](repository/gitignore.template) | `.gitignore` at a code repository's root | Each code repository: it ignores `.env` files |
| [security.md.template](security.md.template) | `SECURITY.md` at a repository's root | Every repository: how to report a vulnerability |
| [codeowners.template](codeowners.template) | `CODEOWNERS` at a repository's root | Every repository: the rule that names its owners, commented out until a person names them |
| [pre-commit-config.yaml.template](pre-commit-config.yaml.template) | `.pre-commit-config.yaml` at a root | A monorepo or a code repository: one local hook that runs `scripts/check` when the repository has one. A workspace keeps its `Makefile`'s `hooks` target |
| [dependabot.yml.template](dependabot.yml.template) | `.github/dependabot.yml` | A repository that has a package manager: the dependency update configuration |
| [workspace/plan/goal.md.template](workspace/plan/goal.md.template) | `plan/goal.md` | A plan for a goal: kind `goal` |
| [workspace/plan/plan.md.template](workspace/plan/plan.md.template) | `plan/plan.md` | A plan: kind `plan`, with its document list, a draft decision, and an adoption's `[[left_out]]` |
| [workspace/plan/repositories.md.template](workspace/plan/repositories.md.template) | `plan/repositories.md` | A project of several repositories: kind `repositories`, the manifest |
| [workspace/plan/requirements.md.template](workspace/plan/requirements.md.template) | `plan/requirements.md` | Product requirements: kind `requirements`, with open and met requirements and the `QA-1` requirement every verify criterion names |
| [workspace/plan/spec.md.template](workspace/plan/spec.md.template) | `plan/specs/<id>.md` | A technical spec: kind `spec` |
| [workspace/plan/architecture.md.template](workspace/plan/architecture.md.template) | `plan/architecture/<id>.md` | An architecture document: kind `architecture` |
| [workspace/plan/experience-design.md.template](workspace/plan/experience-design.md.template) | `plan/design/<id>.md` | An experience design: kind `experience-design`, with its assets |
| [workspace/plan/milestone.md.template](workspace/plan/milestone.md.template) | `plan/milestones/<id>.md` | A milestone: kind `milestone`, with exit criteria |
| [workspace/plan/shared-contract.md.template](workspace/plan/shared-contract.md.template) | `plan/contracts/<id>.md` | A contract other contracts build on: kind `contract` with `shared = true` |
| [workspace/plan/contract.md.template](workspace/plan/contract.md.template) | `plan/contracts/<id>.md` | A contract: kind `contract`, with criteria mapped to requirements, `uses`, `documents`, `reads`, `decisions`, and a work order with every key it can set |
| [workspace/docs/decision.md.template](workspace/docs/decision.md.template) | `docs/decisions/NNNN-short-title.md` | An architecture decision record: kind `decision`, with a status that starts as `proposed`. An accepted record answers a question at the first rung of the question ladder |
| [workspace/docs/status.md.template](workspace/docs/status.md.template) | `docs/status.md` | The current focus: intent only, never the state of work |
| [workspace/docs/glossary.md.template](workspace/docs/glossary.md.template) | `docs/glossary.md` | The project's terms |
| [repository/profile.toml.template](repository/profile.toml.template) | `.ropeshift/profile.toml` in a code repository | A repository that needs gates, a lower cap, a review bound, or a setup step for each lane worktree |
| [repository/catalog-info.yaml.template](repository/catalog-info.yaml.template) | `catalog-info.yaml` at a repository's root, or in each package folder | A repository whose components ropeshift should know: Backstage `kind: Component` entities with a purpose, boundaries, paths, dependencies, and tracker identifiers in `ropeshift.com/` annotations. ADR-0028 is accepted, and ropeshift reads the file from step C0 |
| [user/profile.toml.template](user/profile.toml.template) | `profile.toml` in the installation's state directory | Every machine that runs lanes: the worktree root, `push_on_stop`, and the lane caps |
| [policies/solo/](policies/solo/) | Published with `ropeshift policy publish --file` | One person: delegation of the two check commands, and desktop notices |
| [policies/team/](policies/team/) | Published with `ropeshift policy publish --file` | A team: delegation of the two check commands, a stricter decision policy, parking on one-way decisions, a delegate policy that keeps lane cost with members, and a signed webhook in place of desktop notices |

## The plan file templates

The plan templates describe one project, so they cite each other the way real plan files must: the plan lists every document in `documents`, each document and contract names the plan in `plan`, contracts cite documents as `kind:id`, each criterion names requirement IDs, and each work order names a repository the manifest lists. Use them together, or remove from the plan's `documents` each entry you leave out.

- A plan's `[[decision]]` tables hold the answers given while planning, and approving the plan records each in the ledger. A decision record is a document of kind `decision` with a status, kept in the repository as prose; a contract cites it as `decision:adr-0001`.
- `reads = ["acme-workspace"]` names the workspace by the name registration gives it, its folder's name.
- In a monorepo, leave `repo` out of every work order, drop the manifest, and cite code as `acme:apps/api/handler.go`, with the monorepo's own name.

## The policy templates

Each policy file holds one table and states every key its kind requires. Publish each with `ropeshift policy publish --file <file>`, as a member, after setting `version` to one more than the version in force. A policy a folder leaves out keeps its built-in version. The autonomy and assistant policies belong to the installation, and the built-in versions suit both scales here.

## The test

`templates_test.go` renders every template with the example, with starting data for a project with no repositories, and with starting data for a project with two. It fails when:

- a file whose first line is `+++` does not parse with `repofiles.ParseFile`, or with `repofiles.ParseManifest` for kind `repositories`;
- `user/profile.toml` does not resolve with `profiles.Resolve`, or `repository/profile.toml` does not resolve with `profiles.ResolveRepository` over an empty user layer;
- a policy template is refused by the decode `POST /v1/policies` runs, called in process against a test store;
- `repository/catalog-info.yaml` does not parse as YAML, or a document of it lacks a non-empty `metadata.name` or `metadata.description`;
- a gitignore template lacks the lines `.env`, `.env.*`, and `!.env.example`;
- a template is in this directory and `Names()` does not list it.

Each check is proved by a deliberately broken copy in the test that the check must refuse. `shapes_test.go` commits the files of each shape to a git repository and checks what git reports.

To add a template, add a file that ends in `.template`, and, when it sits in a directory the `go:embed` line in `embed.go` does not name, add the directory to that line.
