// Keep runtime identity separate from how a session was originally launched.
// A KindTerm session may host Codex, Claude, Gemini, or Copilot later on.
export function sessionToolCandidates(session) {
  return [
    session?.tool,
    session?.provider,
    session?.command,
    session?.agent,
    session?.source,
  ].filter(Boolean);
}

export function sessionToolIdentity(session) {
  return sessionToolCandidates(session).join(' ') || (session?.term ? 'bash' : '');
}
