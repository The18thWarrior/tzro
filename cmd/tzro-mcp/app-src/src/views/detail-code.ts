// ============================================================
// views/detail-code.ts — Code node detail body renderer
// Syntax-highlighted output + unified diff rendering
// ============================================================

import type { DetailContext } from './detail-router';
import { registerRenderer } from './detail-router';
import { renderMarkdown } from '../markdown';
import { hljs } from '../highlight';
import { escapeHtml } from '../graph';

/** Render the Code node detail body. */
function renderCodeBody(ctx: DetailContext, container: HTMLElement): void {
  const { nodeState, nodeInfo } = ctx;
  const instructions = nodeInfo?.instructions || '';
  const output = (typeof nodeState !== 'string' ? nodeState?.output : nodeState) || '';

  // Try to extract filepath from instructions or output
  const filepath = extractFilepath(instructions, output);

  // Detect language from filepath extension
  const lang = filepath ? detectLanguage(filepath) : '';

  // Detect if output is structured diff hunks (JSON array with searchContent/replaceContent)
  const hunks = parseDiffHunks(output);

  let html = '';

  // Instructions
  if (instructions) {
    html += `
      <details class="detail-section" open>
        <summary>Instructions</summary>
        <div class="detail-section-content">${renderMarkdown(instructions)}</div>
      </details>
    `;
  }

  // Filepath badge
  if (filepath) {
    html += `<div class="filepath-badge">📄 ${escapeHtml(filepath)}</div>`;
  }

  if (hunks && hunks.length > 0) {
    // Diff mode: render structured hunks as unified diff
    html += renderDiffHunks(hunks, output);
  } else if (output) {
    // Full mode: render syntax-highlighted code
    html += renderCodeBlock(output, lang);
  }

  container.innerHTML = html;

  // Wire copy buttons
  container.querySelectorAll('.copy-btn').forEach((btn) => {
    btn.addEventListener('click', () => {
      const text = btn.getAttribute('data-copy') || '';
      navigator.clipboard.writeText(text).then(() => {
        btn.classList.add('copied');
        btn.textContent = 'Copied ✓';
        setTimeout(() => {
          btn.classList.remove('copied');
          btn.textContent = 'Copy';
        }, 1500);
      });
    });
  });
}

/** Render a syntax-highlighted code block with copy button. */
function renderCodeBlock(code: string, lang: string): string {
  let highlighted: string;
  if (lang && hljs.getLanguage(lang)) {
    highlighted = hljs.highlight(code, { language: lang }).value;
  } else {
    highlighted = hljs.highlightAuto(code).value;
  }

  return `
    <div class="code-block-wrapper">
      <button class="copy-btn" data-copy="${escapeAttr(code)}">Copy</button>
      <pre><code class="hljs">${highlighted}</code></pre>
    </div>
  `;
}

/** Diff hunk shape from tzro_code diff mode. */
interface DiffHunk {
  searchContent: string;
  replaceContent: string;
}

/** Try to parse output as structured diff hunks. */
function parseDiffHunks(output: string): DiffHunk[] | null {
  if (!output.trim().startsWith('[') && !output.trim().startsWith('{')) return null;

  try {
    const parsed = JSON.parse(output);
    const arr = Array.isArray(parsed) ? parsed : [parsed];

    // Validate it looks like diff hunks
    if (arr.length > 0 && arr[0].searchContent !== undefined && arr[0].replaceContent !== undefined) {
      return arr as DiffHunk[];
    }
  } catch { /* not JSON */ }

  return null;
}

/** Render diff hunks as unified diff blocks. */
function renderDiffHunks(hunks: DiffHunk[], rawOutput: string): string {
  let html = hunks.map((hunk, i) => {
    const removeLines = hunk.searchContent.split('\n').map(
      (line) => `<div class="diff-line diff-line-remove">- ${escapeHtml(line)}</div>`
    ).join('');
    const addLines = hunk.replaceContent.split('\n').map(
      (line) => `<div class="diff-line diff-line-add">+ ${escapeHtml(line)}</div>`
    ).join('');

    return `
      <div class="diff-hunk">
        <div class="diff-hunk-header">Hunk ${i + 1} of ${hunks.length}</div>
        ${removeLines}
        ${addLines}
      </div>
    `;
  }).join('');

  // Add a copy button for the full raw output
  html = `
    <div class="code-block-wrapper">
      <button class="copy-btn" data-copy="${escapeAttr(rawOutput)}">Copy</button>
      ${html}
    </div>
  `;

  return html;
}

/** Extract filepath from instructions or output text. */
function extractFilepath(instructions: string, _output: string): string {
  // Look for common patterns: "filepath: ...", "file: ...", "path: ..."
  const patterns = [
    /filepath[:\s]+["']?([^\s"']+)/i,
    /(?:target|file|path)[:\s]+["']?([^\s"']+\.\w+)/i,
    /📄\s*([^\s]+)/,
  ];

  for (const p of patterns) {
    const m = instructions.match(p);
    if (m) return m[1];
  }

  return '';
}

/** Detect language from file extension. */
function detectLanguage(filepath: string): string {
  const ext = filepath.split('.').pop()?.toLowerCase() || '';
  const map: Record<string, string> = {
    go: 'go',
    ts: 'typescript',
    tsx: 'typescript',
    js: 'javascript',
    jsx: 'javascript',
    py: 'python',
    json: 'json',
    css: 'css',
    sql: 'sql',
  };
  return map[ext] || '';
}

/** Escape string for use in HTML attributes. */
function escapeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// Register for composite check (outputFormat === "source_code")
registerRenderer('code', renderCodeBody);
