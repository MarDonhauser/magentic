# Represent unavailable observations explicitly

Session Observation and Repositories return known, partial, and unavailable
facts instead of translating probe failure into absence, death, cleanliness, or
zero divergence. Consumers may preserve a last successful view, but decisions
that destroy or overwrite work require fresh known facts.

Desktop Worktree actions carry an opaque `WorktreeRef`; Repositories resolves
it against a fresh topology at action time. Successful but malformed Git output
is unavailable knowledge, not an empty clean result.
