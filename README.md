# Spec CLI

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

Git is required. Run `spec init` in a repository with an initial commit.

1. Define the change and acceptance criteria.
2. Create a plan or generate an implementation prompt.
3. Implement the change.
4. Review changes and verification evidence.
5. Complete the change.

Spec stores the active change in `.spec.md` at the repository root. This file is excluded through `.git/info/exclude`.

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

## Commands

```text
spec                      Open or resume the interactive workflow.
spec init                 Register the current Git workspace.
spec configure            Open global configuration and templates.
spec new [title]          Start or resume guided Spec setup.
spec prompt [--info]      Print a bounded, provider-neutral prompt.
spec prompt --plan        Print the planning-only prompt.
spec prompt --include-files
                          Include relevant file contents in the prompt.
spec prompt --copy        Copy the prompt to the system clipboard.
spec plan submit --stdin  Validate and store a ChangePlan read from stdin.
spec verify               Run checks and record the workspace fingerprint.
spec done [summary]       Explicitly finish and archive the active change.

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

## Configuration

The first `spec init` creates default configuration files. On Linux, they are in `~/.config/spec/`:

```text
config.yml
templates/
  adr.md
  readme.md
  runbook.md
```

Use `spec configure` to edit these files. The ADR template supports `{{.Number}}` and `{{.Title}}`. The runbook template supports `{{.Title}}`.

Configure verification commands in `config.yml`:

```yaml
verify:
  - go test ./...

workspaces:
  /absolute/path/to/a/project:
    verify:
      - pytest
      - ruff check .
```

## Prompts and plans

Generate a planning prompt:

```sh
spec prompt --plan --copy
```

Submit the returned plan in the CLI or through standard input. A plan contains one `spec-plan` block:

````text
```spec-plan
{"summary":"Describe the implementation","files":[{"path":"internal/example.go","action":"modify","reason":"Implement the requested behaviour"}],"integration_points":[],"verification":[],"uncertainties":[]}
```
````

An agent with terminal access can submit the same JSON:

```sh
spec plan submit --stdin <<'JSON'
{"summary":"Describe the implementation","files":[{"path":"internal/example.go","action":"modify","reason":"Implement the requested behaviour"}]}
JSON
```

Use `spec prompt --copy` to generate an implementation prompt. Amend a plan when its scope changes after implementation starts.

## Review and history

Review compares the current Git state with the recorded starting state. Run `spec verify` to record check results. `spec done` archives the definition, plans, changes, and evidence.

## Project documents

`spec readme` creates `README.md` in the current directory. `spec adr "Title"` creates a numbered decision record in `docs/adr/`. `spec runbook "Scenario"` creates or updates a procedure in `docs/runbooks/`.

## Prompt context

Add paths to `## Relevant Files` in `.spec.md`. Use `spec prompt --include-files` to include their contents in a prompt.

## Docker Sandbox

`spec sandbox` runs the Git workspace with Docker Sandbox. For supported agents, `spec usage` reports sandbox usage and `spec usage history` reports archived usage.

## Development

Building from source requires Go 1.25 or later.

```sh
./scripts/verify.sh
make build
make dev
make dist VERSION=v0.1.0
```

`make dev` builds the current checkout as version `dev` and installs it to your local binary directory.

## Release

Run the release command from a clean `main` branch:

```sh
make release
```
