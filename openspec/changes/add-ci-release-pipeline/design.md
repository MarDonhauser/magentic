## Context

The repository has no `.github/workflows/`. Builds and tests run only on developer machines. Measured on 2026-10-28 with Go 1.26.4 (the version `go.mod` and `app/go.mod` declare):

| Suite | Command | Result |
|---|---|---|
| Root Go module | `go vet ./... && go test ./...` | 2 failures in `core`, rest passes |
| App Go module | `cd app && go test ./...` | passes |
| Frontend | `cd app/frontend && npm test` | 157/157 pass |

Both failures are `SwitchVendor` tests in `core/provider_switch_test.go`. `SessionLifecycle.SwitchVendor` (`core/lifecycle.go:517`) calls `providerBinaryAvailable`, which runs `exec.LookPath` against the real `PATH`. The tests pass only where `codex` happens to be installed.

The desktop build was reproduced locally, step by step as the pipeline would run it: `wails build -platform darwin/universal` with Wails 2.12.0 produces an `x86_64 arm64` binary in about a minute, the post-build hook signs it, and `hdiutil create … -format UDZO` yields a 16 MB DMG that `hdiutil verify` accepts. Under Go 1.27 the same build fails in binding generation (`internal error: package "context" without types`).

`app/wails.json` sets no `info` block, so the bundle's `CFBundleShortVersionString` is the Wails default. `app/build/darwin/Info.plist` already takes the version from `{{.Info.ProductVersion}}`.

## Goals / Non-Goals

**Goals:**
- One workflow file that both guards changes and produces releases, so the release path cannot drift from the tested path.
- Every test passes on a fresh runner without installing agent CLIs.

**Non-Goals:**
- Running the suites on Linux or Windows. The app module contains darwin-only cgo code; a cross-platform matrix is a separate decision.
- Caching Wails or pre-built toolchains beyond what the setup actions provide.

## Decisions

### One workflow, two jobs: `test` and `release`

`.github/workflows/ci.yml` holds a `test` job and a `release` job with `needs: test`. Triggers: `push` to `main`, `push` of tags `v*`, `pull_request` to `main`, and `workflow_dispatch`. `release` runs only for tag pushes and manual runs (`if: startsWith(github.ref, 'refs/tags/v') || github.event_name == 'workflow_dispatch'`). Only a tag push publishes; a manual run uploads the DMG as a run artifact.

*Alternative:* separate `ci.yml` and `release.yml`. Rejected: a tag push would run the tests in one workflow and build in another with no ordering guarantee between them, so a red tag could still be released.

### Both jobs on `macos-14`

The app module's tests compile darwin cgo files (`notch_darwin.m`, `badge_darwin.go`), so they need macOS. Running the root module on a cheaper Linux runner would split one `test` job into two runner types for little gain at the current test duration (about 25 s locally).

*Alternative:* root module on `ubuntu-latest`, app module on macOS. Deferred until macOS minutes become a cost concern.

### Go from `go-version-file`, Wails pinned to v2.12.0

`actions/setup-go` reads `app/go.mod`, so the toolchain follows the repository rather than the runner image. Wails is installed with `go install …/wails@v2.12.0`, the version in `app/go.mod`. This avoids the Go 1.27 binding failure; the README records the constraint for local builds.

### Inject the install check into `SessionLifecycle`

`SessionLifecycle` gains an `installed func(AgentProvider) bool` field, set to `providerBinaryAvailable` by `OpenSessionLifecycle` and replaced in `lifecycleHarness` with a function that reports every provider as installed. This mirrors what `ControlService` already does with its `installed` field (`core/control_dispatch.go:68`). The test for the missing-binary path (`provider_switch_test.go:112`) sets the field to report absence, instead of relying on the machine not having the binary.

*Alternative:* put a stub `codex` script on the runner's `PATH`. Rejected: it hides the dependency instead of removing it, and the next vendor test fails the same way.

### Ad-hoc signing through `MAGENTIC_SIGN_IDENTITY=-`

`scripts/sign-app.sh` treats the identity `-` as ad-hoc: `codesign --force --deep --sign - --identifier com.wails.magentic`, skipping the certificate lookup. The release job sets the variable; local builds do not and keep signing with `magentic-dev`. The post-build hook in `app/wails.json` stays the only place signing happens.

### Version stamped into `wails.json` before the build

`app/wails.json` gets `"info": {"productVersion": "0.0.0-dev"}`. The release job derives the version from the tag (`v0.3.0` → `0.3.0`; on a manual run, `0.0.0-<short sha>`) and writes it with `jq` before `wails build`. The change is local to the runner and never committed.

*Alternative:* `-ldflags` only. Rejected: it sets a Go variable, not the bundle's `Info.plist`, which is what Finder shows.

### DMG with `hdiutil`, release with `gh`

The job stages `magentic.app` and a symlink to `/Applications` in a temporary directory and runs `hdiutil create -volname magentic -format UDZO`. Publishing uses `gh release create "$TAG" --generate-notes`, falling back to `gh release upload --clobber` when the release exists. The workflow needs `permissions: contents: write`.

*Alternative:* `create-dmg` or a third-party release action. Rejected: `hdiutil` and `gh` ship on the runner, and neither adds a supply-chain dependency.

## Risks / Trade-offs

- [Ad-hoc signature] → Gatekeeper blocks the first launch, and macOS asks for microphone permission again after each update. The README documents the workaround. A stable identity from a repository secret is the follow-up if this becomes a burden.
- [macOS runner minutes count tenfold in private repositories] → Tests run in about a minute; a release adds about two. If cost matters, the Linux split above is the first lever.
- [Wails binding generation breaks on future Go releases] → Pinned by `go-version-file`. Raising the Go version in `go.mod` is the moment to recheck Wails.
- [`wails build` regenerates `frontend/wailsjs/go/models.ts`] → In CI this is harmless. Locally the committed bindings are already behind the Go structs (the 2026-10-28 build added `model`, `modelProvider`, `cost`, `costKnown`). That drift is outside this change.
- [`go vet` may flag code on a runner that passes locally] → The same Go version is used in both places; any finding is a real one.

## Migration Plan

1. Merge with `workflow_dispatch` available; start a manual run on the branch to confirm the DMG before any tag exists.
2. Push the first tag (`v0.1.0`) once `main` is green.
3. Rollback: delete the workflow file. Nothing else depends on it; `sign-app.sh` and `wails.json` changes are inert without it.
