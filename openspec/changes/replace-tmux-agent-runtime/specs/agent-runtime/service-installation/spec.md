## Purpose

Keeps the daemon running across logins, because managed Sessions outlive the interface only for as long as the daemon does — and makes the state of that arrangement inspectable instead of assumed.

## ADDED Requirements

### Requirement: The daemon can be installed as a login service

Magentic SHALL offer installing its daemon as a user-level service that starts at login and restarts if it exits.

Installation SHALL be an explicit developer action. Magentic MUST NOT install a service as a side effect of starting an interface, of creating a Session, or of updating.

Installation SHALL be user-level only. Magentic MUST NOT install a system-wide service and MUST NOT request elevated privileges.

#### Scenario: Installing is deliberate

- **WHEN** an interface starts, a Session is created, or Magentic is updated
- **THEN** no service is installed

#### Scenario: The service starts at login

- **WHEN** the service is installed and the developer logs in
- **THEN** the daemon is running without any interface being opened

### Requirement: The service state is inspectable and honest

Magentic SHALL report whether the service is installed, whether the daemon is running, and, when it is running, whether it is the daemon this installation manages.

A daemon that is running but was not started by the installed service SHALL be reported as such rather than as the service running.

When the service is not installed, Magentic SHALL say so wherever it claims that managed Sessions outlive the interface, and MUST NOT make that claim unconditionally.

#### Scenario: An unmanaged daemon is named

- **WHEN** a daemon is running that the installed service did not start
- **THEN** the reported state distinguishes it from the service's own daemon

#### Scenario: The durability claim is conditional

- **WHEN** the service is not installed and managed Sessions are presented
- **THEN** the presentation states that they end when the daemon does
- **AND** it offers installing the service

### Requirement: Removing the service leaves Sessions and their work alone

Removing the service SHALL stop it from starting at login and SHALL leave every Session record, Worktree, working directory and vendor conversation record untouched.

Removal SHALL state what happens to running managed processes before it is carried out.

#### Scenario: Removal keeps the records

- **WHEN** the service is removed
- **THEN** every Session record and its work on disk is unchanged

#### Scenario: Removal says what it will do to running work

- **WHEN** removal is requested while managed Sessions are running
- **THEN** the effect on those running processes is stated before removal proceeds

### Requirement: One daemon owns the managed processes

At most one Magentic daemon SHALL own managed processes on a machine. A second daemon that finds the first one alive SHALL refuse to take ownership and SHALL say so, rather than starting a competing set of processes.

#### Scenario: A second daemon refuses ownership

- **WHEN** a Magentic daemon starts while another one already owns the managed processes
- **THEN** it refuses to take ownership
- **AND** it states that another daemon holds it
- **AND** it starts no agent process
