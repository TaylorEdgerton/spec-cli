# Spec-cli

Spec is a lightweight CLI for structured AI-assisted development.

It provides a simple, standardised workflow for working with AI, whether the repository already has skills, `CLAUDE.md`, or `AGENTS.md`, or you are just using AI to help with a tricky SQL query or a small script change.

You may already be used to the familiar **Copy → Paste → Prompt → Copy → Paste** workflow, or you may not want to hand over your codebase and understanding to a fully autonomous agent harness.

Spec adds a little more structure around that existing workflow. A clear spec for the work, Git-tracked changes, repeatable checks, and templates for things like ADRs, READMEs, and runbooks.

The goal is to make AI-assisted development more consistent and easier for humans to follow, without changing the way you work more than necessary.

## Workflow

```sh
spec init
spec new "Fix database reconnect handling"
spec prompt

# Work with the AI tool and editor that you prefer.

spec verify
spec done
```

Git is required, run `spec init` to create a Git repository if required although `spec new` requires a baseline commit.

The active change is stored in `.spec.md` at the repository root which is excluded from the repo.

## Commands

```text
spec init                 Register the current Git workspace.
spec new [title]          Create and activate a change specification.
spec prompt [--info]      Print a bounded, provider-neutral prompt.
spec prompt --copy        Copy the prompt to the system clipboard.
spec verify               Run configured deterministic checks.
spec done [summary]       Finish the active change and record its result.

spec adr "Title"          Create an ADR in docs/adr/.
spec readme               Create or prepare README.md in the current directory.
spec runbook              Create or prepare docs/runbook.md for an update.
spec sandbox [agent]      Run the Git workspace with Docker Sandbox.
spec usage                Report AI usage for the active Spec sandbox.
spec usage history        Show usage for completed Specs in this workspace.
spec check                Report workspace readiness and warnings.
spec uninstall            Remove Spec and installer-owned PATH setup.
```

## State and configuration

Spec associates each workspace with its GIT repository. It stores metadata, generated prompts, verification results, completed specification archives, and short history records in the user state directory. The editable active specification stays in the workspace as `.spec.md` which should be the agents current prompt reference.

`config.yml` configuration file is used to define spec verification tasks. On Linux, the default path is `~/.config/spec/config.yml`.

```yaml
verify:
  - go test ./...

workspaces:
  /absolute/path/to/a/project:
    verify:
      - pytest
      - ruff check .
```

If there is no configured command, Spec detects a small set of common project checks. It gives priority to `scripts/verify.sh`, `make test`, and language-standard test commands.

## Prompt context

Add file paths as list items in `.spec.md` under `## Relevant Files`. The `spec prompt` output tells the AI to read `.spec.md`, then adds selected files, a bounded Git diff, and the latest failed verification output when useful. It does not duplicate the full specification, repository, Git history, or AI conversations.

## Docker Sandbox

`spec sandbox` scans the complete workspace for sensitive file names to outline just in case, including Git-ignored and untracked files. The command then uses Docker's `sbx` command with the Git working tree.

For `claude`, `codex`, `copilot`, and `gemini` sandboxes, Spec enables native OpenTelemetry metrics and sends them to a collector inside the sandbox. Run `spec usage` to see the current usage. `spec done` stores the final provider/model token and request totals in external Spec history, which `spec usage history` displays newest-first for the current Git workspace.

## Development

```sh
./scripts/verify.sh
make build
make dist VERSION=v0.1.0
```

Generated binaries are in `bin/` and `dist/`. Do not commit them.

## Release

Run the release command from a clean `main` branch:

```sh
make release
```

## Install

Linux and macOS:

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
