## Purpose

Checks every change to Magentic automatically against all of its test suites, so a regression is visible before it reaches `main` and no release is built from a failing commit.

## ADDED Requirements

### Requirement: Every change to main and every pull request is checked

The pipeline SHALL run on each push to `main` and on each pull request targeting `main`. It SHALL NOT run on a schedule.

#### Scenario: A push to main is checked

- **WHEN** a commit is pushed to `main`
- **THEN** the pipeline runs the test suites against that commit and reports the result on it

#### Scenario: A pull request is checked

- **WHEN** a pull request targeting `main` is opened or updated
- **THEN** the pipeline runs the test suites against the pull request's head and reports the result on the pull request

### Requirement: The check covers every test suite in the repository

A pipeline run SHALL execute static checks and tests of the root Go module, tests of the desktop app's Go module, and the frontend tests. The run SHALL fail if any one of them fails, and its log SHALL show which suite failed.

#### Scenario: A failing frontend test fails the run

- **WHEN** a commit breaks one frontend test while all Go tests pass
- **THEN** the pipeline run fails and names the frontend suite as the failure

#### Scenario: All suites pass

- **WHEN** every suite passes for a commit
- **THEN** the pipeline run succeeds

### Requirement: The suites run on a clean machine

Every test SHALL pass on a machine that has only the build toolchain installed. No test SHALL depend on a coding-agent CLI (Claude Code, Codex, Gemini CLI, GitHub Copilot) being installed on the machine that runs it.

The fail-closed behavior for a missing agent CLI MUST stay unchanged at runtime: a Session for a vendor whose CLI is absent still cannot be started.

#### Scenario: Vendor switching is tested without the vendor's CLI

- **WHEN** the tests run on a machine without the `codex` CLI
- **THEN** the tests that switch a Session to Codex pass

#### Scenario: A missing CLI still blocks a real Session

- **WHEN** a developer switches a Session to a vendor whose CLI is not on `PATH`
- **THEN** the switch is refused with the message that the vendor is not installed

### Requirement: The pipeline builds with the pinned toolchain

The pipeline SHALL build and test with the Go version declared by the repository's Go modules, not with the newest available release.

#### Scenario: A newer Go release appears

- **WHEN** a Go release newer than the declared version becomes available on the runner
- **THEN** the pipeline still uses the declared version
