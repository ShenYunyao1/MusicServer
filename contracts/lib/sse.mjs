// Parse a text/event-stream transcript into the small frame model used by the
// contract. Only the fields this protocol emits are retained; the server never
// sends ids or retry hints.

export function parseSSE(text) {
  const normalized = text.replace(/\r\n?/g, '\n');
  const frames = [];
  for (const block of normalized.split('\n\n')) {
    if (block.trim() === '') continue;
    let event = null;
    const data = [];
    let comments = [];
    for (const line of block.split('\n')) {
      if (line.startsWith(':')) {
        comments.push(line.slice(1).trim());
        continue;
      }
      if (line === '') continue;
      const colon = line.indexOf(':');
      const field = colon === -1 ? line : line.slice(0, colon);
      let value = colon === -1 ? '' : line.slice(colon + 1);
      if (value.startsWith(' ')) value = value.slice(1);
      if (field === 'event') event = value;
      else if (field === 'data') data.push(value);
    }
    if (event === null) {
      if (comments.length > 0) frames.push({ type: 'heartbeat' });
      continue;
    }
    const raw = data.join('\n');
    frames.push({ type: 'event', event, data: raw === '' ? null : JSON.parse(raw) });
  }
  return frames;
}

export function serializeFrame(frame) {
  if (frame.type === 'heartbeat') return ': heartbeat\n\n';
  if (frame.type !== 'event') throw new Error(`unknown frame type ${frame.type}`);
  return `event: ${frame.event}\ndata: ${JSON.stringify(frame.data ?? {})}\n\n`;
}
