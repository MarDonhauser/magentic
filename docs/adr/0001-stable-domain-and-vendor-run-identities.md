# Use stable domain IDs and vendor-qualified run identities

Projects and Sessions receive durable Magentic IDs that are independent of
names, filesystem locations, and runtime names. Coding-agent run identities use
`AgentRunRef`, qualified by vendor, because those external identifiers support
resume and transcript correlation but are neither Magentic Session identities
nor globally unique across vendors; this preserves associations across rename
and runtime replacement while preventing cross-vendor collisions.

Desktop and TUI actions transport `ProjectID` and `SessionID`; names are labels
and optional postconditions, never lookup authority. Persisted Dock tabs follow
the same rule. Tabs written before stable IDs were introduced cross one explicit
name-based migration Adapter, are resolved only to registered Dock Sessions,
and are immediately persisted again with their `SessionID`.
