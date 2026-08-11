// ============================================================
// views/detail-generic.ts — Generic detail body renderer (fallback)
// Produces only the middle content: instructions, output, tool dispatch
// ============================================================

import type { DetailContext } from './detail-router';
import { escapeHtml } from '../graph';
import { renderMarkdown } from '../markdown';

/** Render the generic detail body (instructions + output + tool dispatch). */
export function renderGenericBody(ctx: DetailContext, container: HTMLElement): void {
  const { nodeState, nodeInfo } = ctx;
  const status = (typeof nodeState !== 'string' ? nodeState?.status : nodeState) || 'pending';
  const instructions = nodeInfo?.instructions || '';
  const output = (typeof nodeState !== 'string' ? nodeState?.output : nodeState) || '';
  const isError = status === 'failed';

  // Parse envelope for tool dispatch info
  let envelope: any = null;
  if (typeof nodeState !== 'string' && nodeState?.structuredOutput) {
    try {
      envelope = JSON.parse(nodeState.structuredOutput);
    } catch { /* not JSON */ }
  }

  const toolsUsed: string[] = envelope?.toolsUsed || [];
  const lastTool = toolsUsed.length > 0 ? toolsUsed[toolsUsed.length - 1] : '';

  let html = '';

  if (instructions) {
    html += `
      <details class="detail-section" open>
        <summary>Instructions</summary>
        <div class="detail-section-content">${renderMarkdown(instructions)}</div>
      </details>
    `;
  }

  if (output && !isError) {
    html += `
      <details class="detail-section" open>
        <summary>Output</summary>
        <div class="detail-section-content">${renderMarkdown(output)}</div>
      </details>
    `;
  }

  if (toolsUsed.length > 0) {
    html += `
      <details class="detail-section">
        <summary>
          Tool Dispatch Log
          <span class="detail-section-meta">${toolsUsed.length} calls${lastTool ? ` · last: ${lastTool}` : ''}</span>
        </summary>
        <div class="detail-section-content">
          <ul class="tool-list">
            ${toolsUsed.map((t) => `<li><span class="tool-name">${escapeHtml(t)}</span></li>`).join('')}
          </ul>
        </div>
      </details>
    `;
  }

  container.innerHTML = html;
}
