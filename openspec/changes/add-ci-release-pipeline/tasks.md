## 1. Tests independent of installed agent CLIs

- [ ] 1.1 Add an `installed func(AgentProvider) bool` field to `SessionLifecycle`, defaulting to `providerBinaryAvailable` in `OpenSessionLifecycle`, and use it in `SwitchVendor` instead of the direct call
- [ ] 1.2 Check the other `providerBinaryAvailable` call sites reachable from lifecycle (`core/lifecycle_tmux.go:98`) and route them through the same field where a test can reach them
- [ ] 1.3 Make `lifecycleHarness` report every provider as installed; make the missing-binary test (`core/provider_switch_test.go:112`) set the field to report absence explicitly
- [ ] 1.4 Verify: with `codex` removed from `PATH` (`PATH=/usr/bin:/bin:$(go env GOROOT)/bin`), `GOTOOLCHAIN=go1.26.4 go test ./...` passes in the repository root

## 2. Signing and versioning for CI builds

- [ ] 2.1 Teach `scripts/sign-app.sh` the ad-hoc identity `-` (sign with `--sign -`, skip the certificate lookup); leave the `magentic-dev` path unchanged
- [ ] 2.2 Add `"info": {"productVersion": "0.0.0-dev"}` to `app/wails.json`
- [ ] 2.3 Verify locally: `MAGENTIC_SIGN_IDENTITY=- wails build -platform darwin/universal` yields an `x86_64 arm64` binary, `codesign --verify --deep` passes, and `Info.plist` carries the stamped version after editing `productVersion`
- [ ] 2.4 Verify `scripts/build-app.sh` still signs with `magentic-dev` on a developer machine

## 3. Pipeline

- [ ] 3.1 Create `.github/workflows/ci.yml` with triggers `push` (`main`, tags `v*`), `pull_request` (`main`), `workflow_dispatch`; no `schedule`
- [ ] 3.2 Add the `test` job on `macos-14`: checkout, `setup-go` from `app/go.mod` with module caching, `setup-node` 22 with npm cache, `go vet ./...` and `go test ./...` in the root, `go test ./...` in `app/`, `npm ci && npm test` in `app/frontend`, each as its own named step
- [ ] 3.3 Add the `release` job with `needs: test`, limited to tag pushes and manual runs: install Wails v2.12.0, stamp the version into `app/wails.json` with `jq`, build with `MAGENTIC_SIGN_IDENTITY=-` and `-platform darwin/universal`
- [ ] 3.4 Package `magentic.app` plus an `/Applications` symlink into `magentic-<version>.dmg` with `hdiutil`, and upload it as a run artifact
- [ ] 3.5 On tag pushes only, publish with `gh release create --generate-notes`, falling back to `gh release upload --clobber`; set `permissions: contents: write`
- [ ] 3.6 Remove the earlier draft `.github/workflows/desktop-dmg.yml`

## 4. Documentation

- [ ] 4.1 Add an "Installation" section near the top of `README.md`: download the DMG from Releases, drag to Applications, open past Gatekeeper (right-click → Open, or `xattr -dr com.apple.quarantine /Applications/magentic.app`), microphone permission per release
- [ ] 4.2 Add a "CI & Releases" subsection under "Build": what runs on push/PR, how to cut a release (`git tag v0.1.0 && git push origin v0.1.0`), manual runs, and the Go 1.26 / Wails 2.12 constraint (Go 1.27 breaks binding generation)

## 5. Verification on GitHub

- [ ] 5.1 Push the branch, open a pull request, and confirm the `test` job runs and passes while `release` is skipped
- [ ] 5.2 Start the workflow manually on the branch and confirm the run offers a DMG artifact and no release is created
- [ ] 5.3 Mount the downloaded DMG on a Mac, install, and confirm the app starts and shows the stamped version
- [ ] 5.4 After merge, push `v0.1.0` and confirm the release carries `magentic-0.1.0.dmg` and generated notes
