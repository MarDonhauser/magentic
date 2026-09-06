export function composeMessage(text, attachments, separator = '\n') {
  const parts = [String(text ?? '').trim()];
  for (const attachment of attachments || []) {
    const path = String(attachment?.path ?? '').trim();
    if (path) parts.push(path);
  }
  return parts.filter(Boolean).join(separator);
}

export function canSendComposer(text, attachments) {
  return composeMessage(text, attachments) !== '';
}
