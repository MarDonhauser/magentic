## Purpose

Turns a tagged commit into a versioned desktop release that anyone with access to the repository can download and install, without cloning or building Magentic themselves.

## ADDED Requirements

### Requirement: A version tag publishes a release

Pushing a tag of the form `v<major>.<minor>.<patch>` SHALL create a GitHub Release named after the tag, with release notes generated from the commits since the previous release, and the desktop app attached as a DMG.

The release SHALL only be built if every test suite passed for the tagged commit. A failing suite SHALL leave no release and no partial artifact behind.

#### Scenario: A tag on a green commit is released

- **WHEN** `v0.3.0` is pushed on a commit whose test suites pass
- **THEN** a GitHub Release `v0.3.0` exists with `magentic-0.3.0.dmg` attached

#### Scenario: A tag on a red commit is not released

- **WHEN** `v0.3.1` is pushed on a commit where a test suite fails
- **THEN** no release `v0.3.1` is created and no DMG is published

#### Scenario: A release build is repeated

- **WHEN** the release build for an existing tag runs again
- **THEN** the DMG on that release is replaced rather than duplicated

### Requirement: The DMG installs the desktop app

The DMG SHALL contain `magentic.app` and a link to the Applications folder, so the app is installed by dragging it there. The app SHALL run natively on Apple Silicon and on Intel Macs.

#### Scenario: Installing from the DMG

- **WHEN** a user opens the DMG
- **THEN** they see `magentic.app` next to an Applications link and can install by dragging one onto the other

#### Scenario: One download for both architectures

- **WHEN** the same DMG is installed on an Apple Silicon Mac and on an Intel Mac
- **THEN** the app starts natively on both

### Requirement: The installed app knows its release version

The app bundle in a release SHALL carry the tag's version (without the leading `v`) as its version, so Finder and the About panel show which release is installed.

#### Scenario: Version from the tag

- **WHEN** the app from release `v0.3.0` is inspected in Finder
- **THEN** its version reads `0.3.0`

### Requirement: The release app is signed without a developer certificate

The release app SHALL be signed ad hoc. It SHALL NOT be notarized. The local build with the stable `magentic-dev` identity SHALL keep working unchanged.

#### Scenario: First launch of a downloaded release

- **WHEN** a user launches the app from a downloaded DMG for the first time
- **THEN** macOS warns that the developer cannot be verified, and the README describes how to open it anyway

#### Scenario: Local builds keep their identity

- **WHEN** a developer runs `scripts/build-app.sh` on a machine with `magentic-dev`
- **THEN** the app is signed with `magentic-dev` as before

### Requirement: A release can be built on demand without publishing

A maintainer SHALL be able to start the release build manually for any branch. A manual run SHALL produce the DMG as a downloadable artifact of that run and SHALL NOT create or change a GitHub Release.

#### Scenario: Trying the build before tagging

- **WHEN** a maintainer starts the build manually on a branch
- **THEN** the run offers a DMG for download and no release appears
