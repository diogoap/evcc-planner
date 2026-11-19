// Notification copy templates (Settings → Notifications): {…} is filled in
// when a notification is sent, […] only appears when it applies.

export interface CopySegment {
  text: string
  kind: 'text' | 'slot' | 'bracket'
}

/** Splits a template into plain text, {placeholders}, and [ ] brackets. */
export function copySegments(template: string): CopySegment[] {
  const out: CopySegment[] = []
  for (const part of template.split(/(\{[^}]*\}|[[\]])/)) {
    if (!part) continue
    const kind = part.startsWith('{') ? 'slot' : part === '[' || part === ']' ? 'bracket' : 'text'
    out.push({ text: part, kind })
  }
  return out
}
