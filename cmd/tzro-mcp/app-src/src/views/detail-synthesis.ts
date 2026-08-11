// ============================================================
// views/detail-synthesis.ts — Synthesis node detail body renderer
// Enhanced generic view with promoted Execution Envelope + full markdown
// ============================================================

import type { DetailContext } from './detail-router';
import { registerRenderer } from './detail-router';
import { renderMarkdown } from '../markdown';
import { fmtDuration, escapeHtml } from '../graph';

/** Render the Synthesis node detail body. */
function renderSynthesisBody(ctx: DetailContext, container: HTMLElement): void {
  const { nodeState, nodeInfo } = ctx;
  const instructions = nodeInfo?.instructions || '';
  const output = (typeof nodeState !== 'string' ? nodeState?.output : nodeState) || '';

  // Parse Execution Envelope
  let envelope: any = null;
  if (typeof nodeState !== 'string' && nodeState?.structuredOutput) {
    try {
      envelope = JSON.parse(nodeState.structuredOutput);
    } catch { /* not JSON */ }
  }

  let html = '';

  // Execution Envelope — promoted to top, expanded (not collapsed)
  if (envelope) {
    const filesRead: string[] = envelope.filesRead || [];
    const filesModified: string[] = envelope.filesModified || [];

    html += `
      <div class="envelope-card-promoted">
        <div class="envelope-card-label">Execution Summary</div>
        <div class="envelope-grid">
          ${envelope.status ? `
            <span class="envelope-label">Status</span>
            <span class="envelope-value">${escapeHtml(envelope.status)}</span>
          ` : ''}
          ${envelope.durationMs != null ? `
            <span class="envelope-label">Duration</span>
            <span class="envelope-value">${fmtDuration(envelope.durationMs)}</span>
          ` : ''}
          ${envelope.nodeCount != null ? `
            <span class="envelope-label">Nodes</span>
            <span class="envelope-value">${envelope.nodesCompleted || 0}/${envelope.nodeCount} completed${envelope.nodesFailed ? `, ${envelope.nodesFailed} failed` : ''}</span>
          ` : ''}
          ${filesModified.length > 0 ? `
            <span class="envelope-label">Files Modified</span>
            <span class="envelope-value">${filesModified.map(f => escapeHtml(f)).join(', ')}</span>
          ` : ''}
          ${filesRead.length > 0 ? `
            <span class="envelope-label">Files Read</span>
            <span class="envelope-value">${filesRead.map(f => escapeHtml(f)).join(', ')}</span>
          ` : ''}
          ${envelope.goalPrompt ? `
            <span class="envelope-label">Goal</span>
            <span class="envelope-value">${escapeHtml(envelope.goalPrompt)}</span>
          ` : ''}
        </div>
      </div>
    `;
  }

  // Instructions
  if (instructions) {
    html += `
      <details class="detail-section" open>
        <summary>Instructions</summary>
        <div class="detail-section-content">${renderMarkdown(instructions)}</div>
      </details>
    `;
  }

  // Synthesis output — full markdown rendering with highlighted code blocks
  if (output) {
    html += `
      <details class="detail-section" open>
        <summary>Synthesis</summary>
        <div class="detail-section-content">${renderMarkdown(output)}</div>
      </details>
    `;
  }

  container.innerHTML = html;
}

// Register
registerRenderer('synthesis', renderSynthesisBody);
