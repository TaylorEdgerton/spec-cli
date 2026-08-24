# Spec-cli

Spec is a lightweight CLI for structured AI-assisted development.

It provides a simple, standardised workflow for working with AI, whether the repository already has skills, `CLAUDE.md`, or `AGENTS.md`, or you are just using AI to help with a tricky SQL query or a small script change.

You may already be used to the familiar **Copy → Paste → Prompt → Copy → Paste** workflow, or you may not want to hand over your codebase and understanding to a fully autonomous agent harness.

Spec adds a little more structure around that existing workflow. A clear spec for the work, Git-tracked changes, repeatable checks, and templates for things like ADRs, READMEs, and runbooks.

The goal is to make AI-assisted development more consistent and easier for humans to follow, without changing the way you work more than necessary.

## Workflow

```sh
spec init
spec

# Work with the AI tool and editor that you prefer.

spec verify
spec
```

Git is required. Run `spec init` to create or register a repository. A baseline commit is required before starting a Spec.

Running `spec` without a command opens the interactive terminal workflow. Run it again after exiting or closing the terminal to resume where you were.

The setup asks the requirement questions. It then reviews success criteria, configures verification, optionally records a baseline, creates `.spec.md`, and provides an implementation prompt to use. Use the arrow keys and Enter to navigate. Use Shift+Tab or the Back item to revisit earlier answers.

The active change is stored in `.spec.md` at the repository root, excluded from commits through `.git/info/exclude`.

## Install

Linux and macOS:

run these while spec is installed to update to the latest version

```sh
curl -fsSL https://raw.githubusercontent.com/TaylorEdgerton/spec-cli/main/install.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/TaylorEdgerton/spec-cli/main/install.ps1 | iex
```

## Uninstall

```sh
spec uninstall
```

## Commands

```text
spec                      Open or resume the interactive workflow.
spec init                 Register the current Git workspace.
spec configure            Open global configuration and templates.
spec new [title]          Start or resume guided Spec setup.
spec prompt [--info]      Print a bounded, provider-neutral prompt.
spec prompt --include-files
                          Include relevant file contents in the prompt.
spec prompt --copy        Copy the prompt to the system clipboard.
spec verify               Run checks and record the workspace fingerprint.
spec done [summary]       Review criteria and finish the active change.

spec adr "Title"          Create an ADR in docs/adr/.
spec readme               Create or prepare README.md in the current directory.
spec runbook              List scenario runbooks.
spec runbook "Scenario"   Create or prepare a scenario runbook.
spec sandbox [agent]      Run the Git workspace with Docker Sandbox.
spec usage                Report AI usage for the active Spec sandbox.
spec usage history        Show usage for completed Specs in this workspace.
spec check                Report workspace readiness and warnings.
spec uninstall            Remove Spec and installer-owned PATH setup.
```

## State and configuration

Spec associates each workspace with its Git repository. It stores setup progress, generated prompts, verification results, completed specification archives, and short history records in the user state directory. The editable active specification is `.spec.md` in the workspace and should be the agent's current prompt reference.

On Linux and macOS, external workspace state defaults to `~/.local/state/spec/projects/<workspace-id>/`. Windows uses the user cache directory under `Spec/State/projects/<workspace-id>/`.

The installer creates the global configuration folder. On Linux, the default path is `~/.config/spec/`. The first `spec init` installs any missing default files:

```text
config.yml
templates/
  adr.md
  readme.md
  runbook.md
```

Run `spec configure` to open the folder to edit the files. Each document command reads its template from this folder every time it runs, so edits apply to the next time around. The ADR template can use `{{.Number}}` and `{{.Title}}`. The runbook template can use `{{.Title}}`.

Interactive verification choices are stored in external workspace state. Existing workspace and global commands in `config.yml` remain available as fallbacks.

```yaml
verify:
  - go test ./...

workspaces:
  /absolute/path/to/a/project:
    verify:
      - pytest
      - ruff check .
```

`spec done` requires a current passing result and asks the engineer to review success criterion before completing the Spec.

## Project documents

`spec readme` uses the global README template to create `README.md` in the current directory. This supports creation in subdirectories.

`spec adr "Title"` creates a numbered decision record in `docs/adr/`. `spec runbook "Scenario"` creates or updates a named procedure such as `docs/runbooks/database-recovery.md`. Run `spec runbook` without a title to list existing runbooks.

## Prompt context

Add file paths as list items in `.spec.md` under `## Relevant Files`. The `spec prompt` helps output a prompt based on `.spec.md`. Use `spec prompt --include-files` to include the contents of the Relevant Files list for pasting into another external chat session.

## Docker Sandbox

`spec sandbox` scans the complete workspace for sensitive file names to outline just in case, The command then uses Docker's `sbx` command with the Git working tree.

For `claude`, `codex`, `copilot`, and `gemini` sandboxes, Spec enables native OpenTelemetry metrics and sends them to a collector inside the sandbox. Run `spec usage` to see the current usage. `spec done` stores the totals in external Spec history, which `spec usage history` displays the history for the workspace.

## Development

Building from source requires Go 1.25 or later.

```sh
./scripts/verify.sh
make build
make dev
make dist VERSION=v0.1.0
```

`make dev` builds the current checkout as version `dev`. If `spec` is already on PATH, it replaces that executable. Otherwise, it installs to `${XDG_BIN_HOME:-$HOME/.local/bin}` and adds the directory to PATH in `~/.profile`.

Generated binaries are in `bin/` and `dist/`. Do not commit them.

## Release

Run the release command from a clean `main` branch:

```sh
make release
```
