# Use stable domain IDs and vendor-qualified run identities

Projects and Sessions receive durable Magentic IDs that are independent of
names, filesystem locations, and runtime names. Coding-agent run identities use
`AgentRunRef`, qualified by vendor, because those external identifiers support
resume and transcript correlation but are neither Magentic Session identities
nor globally unique across vendors; this preserves associations across rename
and runtime replacement while preventing cross-vendor collisions.
