// ============================================================
// views/detail-probe.ts — Probe node detail body renderer
// Synthesis at top, lazy-loaded thought chain timeline below
// ============================================================

import type { DetailContext } from './detail-router';
import { registerRenderer } from './detail-router';
import { renderMarkdown } from '../markdown';
import { fetchNodeSteps } from '../api';
import type { ThoughtStep } from '../api';
import { escapeHtml } from '../graph';

/** Render the Probe node detail body. */
function renderProbeBody(ctx: DetailContext, container: HTMLElement): void {
  const { nodeState, nodeInfo, taskId, nodeId } = ctx;
  const instructions = nodeInfo?.instructions || '';
  const output = (typeof nodeState !== 'string' ? nodeState?.output : nodeState) || '';

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

  // Synthesis output (the answer)
  if (output) {
    html += `
      <details class="detail-section" open>
        <summary>Synthesis</summary>
        <div class="detail-section-content">${renderMarkdown(output)}</div>
      </details>
    `;
  }

  // Thought Chain placeholder (lazy-loaded)
  html += `
    <details class="detail-section" id="tc-section">
      <summary>
        Thought Chain
        <span class="detail-section-meta" id="tc-meta">loading…</span>
      </summary>
      <div class="detail-section-content" id="tc-container">
        <div class="tc-loading"><div class="spinner"></div> Loading steps…</div>
      </div>
    </details>
  `;

  container.innerHTML = html;

  // Wire lazy loading for the thought chain
  const tcSection = container.querySelector('#tc-section') as HTMLDetailsElement;
  if (tcSection) {
    let loaded = false;
    tcSection.addEventListener('toggle', () => {
      if (tcSection.open && !loaded) {
        loaded = true;
        const tcContainer = container.querySelector('#tc-container') as HTMLElement;
        if (tcContainer) {
          renderThoughtChainTimeline(tcContainer, taskId, nodeId);
        }
      }
    });
  }
}

/**
 * Render the thought chain step timeline into a container.
 * Exported for reuse by the Analyze view.
 */
export function renderThoughtChainTimeline(
  container: HTMLElement,
  taskId: string,
  nodeId: string,
  _stepCount?: number,
): void {
  container.innerHTML = '<div class="tc-loading"><div class="spinner"></div> Loading steps…</div>';

  fetchNodeSteps(taskId, nodeId).then((steps) => {
    if (steps.length === 0) {
      container.innerHTML = '<div style="padding:8px;color:var(--text-muted);font-size:12px">No thought chain steps found.</div>';
      // Update the meta text
      const meta = container.closest('.detail-section')?.querySelector('#tc-meta, .detail-section-meta');
      if (meta) meta.textContent = '0 steps';
      return;
    }

    // Update the meta text with step count
    const meta = container.closest('.detail-section')?.querySelector('#tc-meta, .detail-section-meta');
    if (meta) meta.textContent = `${steps.length} steps`;

    container.innerHTML = `<div class="tc-timeline">${steps.map(renderStepCard).join('')}</div>`;
  });
}

/** Render a single thought chain step card. */
function renderStepCard(step: ThoughtStep): string {
  const num = step.stepIndex + 1;
  const toolName = step.toolName || '';
  const thought = step.thought || '';
  const thoughtExcerpt = thought.length > 80 ? thought.substring(0, 80) + '…' : thought;

  // Dot indicator
  let dotClass = 'dot-ok';
  if (!toolName) {
    dotClass = 'dot-synth';
  } else if (!step.toolOutput || step.toolOutput.match(/error|failed|not found/i)) {
    dotClass = 'dot-err';
  }

  const dotIcon = !toolName ? '✦' : '';

  let bodyHtml = '';

  if (thought) {
    bodyHtml += `<div class="tc-step-label">Thought</div><div>${escapeHtml(thought)}</div>`;
  }

  if (step.toolArgs) {
    bodyHtml += `<div class="tc-step-label">Arguments</div><pre>${escapeHtml(step.toolArgs)}</pre>`;
  }

  if (step.toolOutput) {
    bodyHtml += `<div class="tc-step-label">Output${step.truncated ? ' (truncated)' : ''}</div><pre>${escapeHtml(step.toolOutput)}</pre>`;
  }

  return `
    <details class="tc-step">
      <summary>
        ${dotIcon ? `<span style="color:var(--accent);font-size:10px;flex-shrink:0">${dotIcon}</span>` : `<span class="tc-step-dot ${dotClass}"></span>`}
        <span class="tc-step-num">#${num}</span>
        ${toolName ? `<span class="tc-step-tool">${escapeHtml(toolName)}</span>` : ''}
        <span class="tc-step-thought">${escapeHtml(thoughtExcerpt)}</span>
      </summary>
      <div class="tc-step-body">${bodyHtml}</div>
    </details>
  `;
}

// Register
registerRenderer('probe', renderProbeBody);
