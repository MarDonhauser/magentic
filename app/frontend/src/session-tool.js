// Keep runtime identity separate from how a session was originally launched.
// A KindTerm session may host Codex, Claude, Gemini, or Copilot later on.
export function sessionToolIdentity(session) {
  const detected = [
    session?.provider,
    session?.tool,
    session?.command,
    session?.agent,
    session?.source,
  ].filter(Boolean).join(' ');
  return detected || (session?.term ? 'bash' : '');
}
