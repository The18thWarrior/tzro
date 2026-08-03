// ============================================================
// views/overview.ts — Overview screen renderer
// Sticky header, pill strip, compact vertical DAG
// ============================================================

import type { AppStateSnapshot } from '../state';
import { appState } from '../state';
import { topoLayers, nodeStatus, nodeStepDetail, fmtDuration, escapeHtml } from '../graph';
import { cancelTask } from '../api';
import { renderMarkdown } from '../markdown';

/** Render the overview screen into the given container. */
export function renderOverview(container: HTMLElement, state: AppStateSnapshot): void {
  const task = state.taskData;
  if (!task) {
    container.innerHTML = '<div class="error-msg">Task not found. Check daemon connection.</div>';
    return;
  }

  const graph = task.graph || {};
  const nodes = graph.nodes || [];
  const edges = graph.edges || [];
  const states = task.states || {};
  const status = task.status || 'pending';

  // Count node states
  let completed = 0, running = 0, failed = 0, pending = 0;
  nodes.forEach((n) => {
    const s = nodeStatus(n.id, states);
    if (s === 'completed') completed++;
    else if (s === 'running') running++;
    else if (s === 'failed') failed++;
    else pending++;
  });
  const total = nodes.length;
  const pct = total > 0 ? Math.round((completed / total) * 100) : 0;

  // Elapsed time
  const elapsed = state.startTime ? Date.now() - state.startTime : 0;
  const isActive = status === 'running' || status === 'pending';

  // Layers for DAG
  const layers = topoLayers(nodes, edges);
  const nodeMap: Record<string, typeof nodes[0]> = {};
  nodes.forEach((n) => { nodeMap[n.id] = n; });

  // Find synthesis output from terminal/synthesis node
  let synthesis = '';
  Object.entries(states).forEach(([id, s]) => {
    if (s && s.output && nodeMap[id]?.type === 'synthesis') {
      synthesis = s.output;
    }
  });

  // Build HTML
  container.innerHTML = `
    <div class="header">
      <div class="header-left">
        <span class="logo">tzro</span>
        <span class="task-id">${task.taskId?.substring(0, 8) || '—'}</span>
        <span class="status-badge status-${status}">${status}</span>
      </div>
      <div class="header-actions">
        ${isActive ? '<button class="btn-cancel" id="btn-cancel">Cancel</button>' : ''}
      </div>
    </div>

    <div class="pill-strip">
      <span class="pill">◉ ${pct}% · ${completed}/${total} nodes</span>
      ${running > 0 ? `<span class="pill" style="color:var(--blue)">⚡ ${running} running</span>` : ''}
      ${failed > 0 ? `<span class="pill" style="color:var(--red)">✗ ${failed} failed</span>` : ''}
      <span class="pill">⏱ ${fmtDuration(elapsed)}</span>
    </div>

    <div class="dag-container">
      <div class="dag-layers">
        ${layers.map((layer, i) => `
          ${i > 0 ? '<div class="dag-arrow">↓</div>' : ''}
          <div class="dag-layer">
            ${layer.map((id) => {
              const node = nodeMap[id] || { id, type: '?', action: id };
              const s = nodeStatus(id, states);
              const detail = nodeStepDetail(id, states);
              const label = node.action || node.id;
              let stepInfo = '';
              if (detail && 'synthesizing' in detail) {
                stepInfo = `<div class="node-step">✨ Synthesizing (${detail.findings} findings)</div>`;
              } else if (detail && 'step' in detail) {
                const stepPct = Math.round((detail.step / detail.total) * 100);
                stepInfo = `<div class="node-step">
                  <span class="step-label">${detail.step}/${detail.total}: ${detail.tool}</span>
                  <div class="step-bar"><div class="step-fill" style="width:${stepPct}%"></div></div>
                </div>`;
              }
              return `
                <div class="dag-node ${s}" data-node-id="${id}" title="${escapeHtml(node.instructions || label)}">
                  <div class="node-dot ${s}"></div>
                  <span class="node-label">${escapeHtml(label)}</span>
                  <span class="node-type">${node.type || ''}</span>
                  ${stepInfo}
                </div>`;
            }).join('')}
          </div>
        `).join('')}
      </div>
    </div>

    ${synthesis ? `
    <div class="synthesis-section">
      <h3>Synthesis Output</h3>
      <div class="synthesis-content">${renderMarkdown(synthesis)}</div>
    </div>` : ''}
  `;

  // Wire cancel button
  const cancelBtn = container.querySelector('#btn-cancel');
  if (cancelBtn) {
    cancelBtn.addEventListener('click', async () => {
      const btn = cancelBtn as HTMLButtonElement;
      btn.disabled = true;
      btn.textContent = 'Cancelling…';
      if (state.taskId) await cancelTask(state.taskId);
    });
  }

  // Node click → detail view (delegated on container to survive re-renders)
  if (!(container as any).__dagClickWired) {
    container.addEventListener('click', (e) => {
      const target = (e.target as HTMLElement).closest('.dag-node') as HTMLElement | null;
      if (target?.dataset.nodeId) {
        appState.showDetail(target.dataset.nodeId);
      }
    });
    (container as any).__dagClickWired = true;
  }
}
