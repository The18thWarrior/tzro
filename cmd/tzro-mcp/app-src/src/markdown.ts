// ============================================================
// markdown.ts — Markdown rendering via marked + highlight.js
// ============================================================

import { marked } from 'marked';
import { markedHighlight } from 'marked-highlight';
import { hljs } from './highlight';

// Configure marked with syntax highlighting for code blocks
marked.use(markedHighlight({
  langPrefix: 'hljs language-',
  highlight(code: string, lang: string) {
    if (lang && hljs.getLanguage(lang)) {
      return hljs.highlight(code, { language: lang }).value;
    }
    return hljs.highlightAuto(code).value;
  }
}));

marked.setOptions({
  breaks: true,
  gfm: true,
});

/** Render markdown string to HTML. Returns safe HTML string. */
export function renderMarkdown(text: string): string {
  if (!text) return '';
  try {
    return marked.parse(text) as string;
  } catch {
    // Fallback to escaped pre-formatted text
    return `<pre>${escapeHtml(text)}</pre>`;
  }
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
