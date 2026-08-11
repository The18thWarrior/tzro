// ============================================================
// views/detail-router.ts — Detail view coordinator
// Shared chrome (header, errors, envelope) + type→renderer dispatch
// ============================================================

import type { AppStateSnapshot } from '../state';
import { appState } from '../state';
import { nodeStatus, fmtDuration, escapeHtml } from '../graph';
import { renderMarkdown } from '../markdown';
import { renderGenericBody } from './detail-generic';

// ---- Types ----

/** Context passed to every detail body renderer. */
export interface DetailContext {
  nodeId: string;
  nodeInfo: any;       // from graph.nodes[]
  nodeState: any;      // from states[nodeId]
  taskId: string;
  state: AppStateSnapshot;
}

/** Each renderer produces the middle body HTML. */
export type DetailBodyRenderer = (ctx: DetailContext, container: HTMLElement) => void;

// ---- Renderer Registry ----

const renderers: Record<string, DetailBodyRenderer> = {};

/** Register a type-specific body renderer. */
export function registerRenderer(type: string, fn: DetailBodyRenderer): void {
  renderers[type] = fn;
}

/** Resolve the renderer for a given context. */
function getRenderer(ctx: DetailContext): DetailBodyRenderer {
  // Check outputFormat first (code nodes are action nodes with outputFormat)
  if (ctx.nodeInfo?.outputFormat === 'source_code' && renderers['code']) {
    return renderers['code'];
  }
  // Check node type
  const type = ctx.nodeInfo?.type || '';
  if (type && renderers[type]) {
    return renderers[type];
  }
  // Fallback to generic
  return renderers['generic'] || renderGenericBody;
}

// Register the generic renderer as fallback
registerRenderer('generic', renderGenericBody);

// ---- Shared Chrome ----

/** Render the detail view: shared chrome + dispatched body. */
export function renderDetailView(container: HTMLElement, state: AppStateSnapshot): void {
  const task = state.taskData;
  const nodeId = state.selectedNodeId;

  if (!task || !nodeId) {
    container.innerHTML = '<div class="error-msg">No node selected.</div>';
    return;
  }

  const graph = task.graph || {};
  const nodes = graph.nodes || [];
  const states = task.states || {};
  const nodeInfo = nodes.find((n) => n.id === nodeId);
  const nodeState = states[nodeId];
  const status = nodeStatus(nodeId, states);
  const label = nodeInfo?.action || nodeInfo?.id || nodeId;
  const nodeType = nodeInfo?.type || '';

  // Duration
  let duration = '';
  if (nodeState && typeof nodeState !== 'string' && nodeState.completedAt && state.startTime) {
    const ms = nodeState.completedAt * 1000 - state.startTime;
    if (ms > 0) duration = fmtDuration(ms);
  }

  // Error content
  const isError = status === 'failed';
  const output = (nodeState && typeof nodeState !== 'string' ? nodeState.output : nodeState) || '';
  const errorText = isError ? output : '';

  // Execution Envelope
  let envelope: any = null;
  if (nodeState && typeof nodeState !== 'string' && nodeState.structuredOutput) {
    try {
      envelope = JSON.parse(nodeState.structuredOutput);
    } catch { /* not JSON */ }
  }

  // Build shared chrome
  container.innerHTML = `
    <div class="header">
      <div class="header-left">
        <button class="btn-back" id="btn-back">← Back</button>
        <span class="detail-node-name">${escapeHtml(label)}</span>
        ${nodeType ? `<span class="detail-node-type">${nodeType}</span>` : ''}
        <span class="status-badge status-${status}">${status}</span>
        ${duration ? `<span class="duration-badge">${duration}</span>` : ''}
      </div>
    </div>

    <div class="detail-body" id="detail-body-container"></div>

    <div class="detail-footer" id="detail-footer"></div>
  `;

  // Wire back button
  const backBtn = container.querySelector('#btn-back');
  if (backBtn) {
    backBtn.addEventListener('click', () => appState.showOverview());
  }

  // Build context
  const ctx: DetailContext = {
    nodeId,
    nodeInfo: nodeInfo || {},
    nodeState: nodeState || {},
    taskId: task.taskId,
    state,
  };

  // Dispatch body rendering to the matched renderer
  const bodyContainer = container.querySelector('#detail-body-container') as HTMLElement;
  if (bodyContainer) {
    const renderer = getRenderer(ctx);
    renderer(ctx, bodyContainer);
  }

  // Render footer (errors + envelope) — shared across all renderers
  const footerContainer = container.querySelector('#detail-footer') as HTMLElement;
  if (footerContainer) {
    renderFooter(footerContainer, isError, errorText, envelope);
  }
}

/** Render error section + execution envelope (shared footer). */
function renderFooter(
  container: HTMLElement,
  isError: boolean,
  errorText: string,
  envelope: any,
): void {
  const filesRead: string[] = envelope?.filesRead || [];
  const filesModified: string[] = envelope?.filesModified || [];

  let html = '';

  if (isError && errorText) {
    html += `
      <div class="detail-body">
        <details class="detail-section error-section" open>
          <summary>Error</summary>
          <div class="detail-section-content">${escapeHtml(errorText)}</div>
        </details>
      </div>
    `;
  }

  if (envelope) {
    html += `
      <div class="detail-body">
        <details class="detail-section">
          <summary>
            Execution Envelope
            <span class="detail-section-meta">${envelope.durationMs ? fmtDuration(envelope.durationMs) : ''}</span>
          </summary>
          <div class="detail-section-content">
            <div class="envelope-grid">
              ${envelope.status ? `
                <span class="envelope-label">Status</span>
                <span class="envelope-value">${escapeHtml(envelope.status)}</span>
              ` : ''}
              ${envelope.nodeCount != null ? `
                <span class="envelope-label">Nodes</span>
                <span class="envelope-value">${envelope.nodesCompleted || 0}/${envelope.nodeCount} completed${envelope.nodesFailed ? `, ${envelope.nodesFailed} failed` : ''}</span>
              ` : ''}
              ${envelope.durationMs != null ? `
                <span class="envelope-label">Duration</span>
                <span class="envelope-value">${fmtDuration(envelope.durationMs)}</span>
              ` : ''}
              ${filesRead.length > 0 ? `
                <span class="envelope-label">Files Read</span>
                <span class="envelope-value">${filesRead.map(f => escapeHtml(f)).join(', ')}</span>
              ` : ''}
              ${filesModified.length > 0 ? `
                <span class="envelope-label">Files Modified</span>
                <span class="envelope-value">${filesModified.map(f => escapeHtml(f)).join(', ')}</span>
              ` : ''}
              ${envelope.goalPrompt ? `
                <span class="envelope-label">Goal</span>
                <span class="envelope-value">${escapeHtml(envelope.goalPrompt)}</span>
              ` : ''}
            </div>
          </div>
        </details>
      </div>
    `;
  }

  container.innerHTML = html;
}
