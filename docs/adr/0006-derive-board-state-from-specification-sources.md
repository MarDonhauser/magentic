# Derive Board state from Specification sources

Specifications retain a Project- and source-qualified identity and expose
source availability independently from discovered items. Completing every task
moves current work to review, while done requires an archived or explicitly
terminal source state; archived discovery is opt-in and hard-bounded so the
default Board remains current, fast, and truthful.

Source discovery and start resolution enforce physical Project containment so
symlinked source roots cannot escape the Project. Active Board association uses
the durable `SpecificationRef` persisted on a Session plus a known-live
Observation; names, branches, paths, and dead runtimes are not substitutes.
