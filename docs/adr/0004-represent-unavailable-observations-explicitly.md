# Represent unavailable observations explicitly

Session Observation and Repositories return known, partial, and unavailable
facts instead of translating probe failure into absence, death, cleanliness, or
zero divergence. Consumers may preserve a last successful view, but decisions
that destroy or overwrite work require fresh known facts.
