## Why

Magentic has no automated checks and no distributable build. Every test run and every desktop build happens on a developer's machine, and the only way to get the app onto another Mac is to clone the repository and run `scripts/build-app.sh` there. A broken change reaches `main` unnoticed, and there is no versioned artifact anyone can download.

The pieces exist: three test suites (root Go module, `app/` Go module, frontend Node tests) and a Wails build that already signs itself through a post-build hook. What is missing is a pipeline that runs them on every change and turns a tagged commit into a downloadable release.

## What Changes

- Add a GitHub Actions pipeline that runs every test suite on each push to `main` and on each pull request.
- Make the test suites runnable on a clean machine. Two `core` tests (`TestSwitchVendorRestartsAndKeepsRuns`, `TestSwitchVendorToKnownRunResumes`) currently pass only where the `codex` CLI is installed, because the vendor install check reads the real `PATH`.
- On a version tag (`v*`), build `magentic.app` as a universal binary (Apple Silicon and Intel), package it as a DMG, and publish it as a GitHub Release. The release build runs only after the test suites pass on the same commit.
- Stamp the tag's version into the app bundle, so `CFBundleShortVersionString` reflects the release instead of the Wails default.
- Let `scripts/sign-app.sh` sign ad hoc when no stable signing identity exists, so the post-build hook does not fail on a CI runner.
- Document installation from a release, the Gatekeeper step for an unnotarized app, and how a release is cut, in `README.md`.

### Non-goals

- Notarization or a Developer ID signature. The DMG is ad-hoc signed; Gatekeeper warns on first launch.
- A stable signing identity in CI. Microphone permission is therefore granted again after each installed release.
- Releasing the TUI binary (`magentic`). Only the desktop app is packaged.
- Scheduled or nightly runs.
- Automatic updates inside the app.

## Capabilities

### New Capabilities

- `delivery/continuous-integration`: which changes are checked, which suites run, and what a failing suite blocks.
- `delivery/desktop-release`: how a version tag becomes a versioned, downloadable DMG on a GitHub Release, and what that artifact contains.

### Modified Capabilities

<!-- None. No published spec covers build or delivery. -->

## Impact

- `.github/workflows/`: new pipeline definition (the repository has none today).
- `scripts/sign-app.sh`: ad-hoc signing path; local builds with `magentic-dev` are unchanged.
- `core/provider.go`, `core/provider_switch_test.go`: the vendor install check becomes replaceable in tests. No production behavior changes; a missing binary still fails closed.
- `app/wails.json`: gains a `productVersion` the release build overwrites from the tag.
- `README.md`: new installation section and a CI/release subsection under Build.
- Build toolchain pinning: the pipeline uses the Go version from `go.mod` (1.26.4). Wails 2.12.0 fails under Go 1.27 (`package "context" without types`), which the README must state.
