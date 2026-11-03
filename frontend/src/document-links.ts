export interface DocumentLink { start: number; end: number; label: string; target: string }

// Parentheses in a filename belong to the target, so a closing Markdown
// delimiter is the one that balances its opening delimiter.
export function documentLinks(text: string): DocumentLink[] {
  const links: DocumentLink[] = [];
  const opening = /(?<!!)\[([^\]\n]+)\]\(/g;
  for (const match of text.matchAll(opening)) {
    const start = match.index + match[0].length;
    let depth = 1;
    let end = start;
    for (; end < text.length && depth && text[end] !== "\n"; end++) {
      if (text[end] === "(") depth++;
      else if (text[end] === ")") depth--;
    }
    if (depth) continue;
    const target = text.slice(start, end - 1);
    let decoded: string;
    try { decoded = decodeURIComponent(target); }
    catch { continue; }
    if (!/^(?:[a-z]:[\\/]|\/(?![\\/]))/i.test(decoded) || !/\.pdf$/i.test(decoded) || /[<>"|?*]/.test(decoded) || [...decoded].some((character) => character.charCodeAt(0) < 32)) continue;
    links.push({ start: match.index, end, label: match[1], target });
  }
  return links;
}
