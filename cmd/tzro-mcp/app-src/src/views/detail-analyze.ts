// ============================================================
// views/detail-analyze.ts — Analyze node detail body renderer
// Composes over Probe view: synthesis + evidence tables + thought chain
// ============================================================

import type { DetailContext } from './detail-router';
import { registerRenderer } from './detail-router';
import { renderThoughtChainTimeline } from './detail-probe';
import { renderMarkdown } from '../markdown';
import { hljs } from '../highlight';
import { escapeHtml } from '../graph';

/** Analytical evidence item shape. */
interface EvidenceItem {
  sql: string;
  rows: Record<string, any>[];
  totalRows: number;
  capped: boolean;
}

/** Render the Analyze node detail body. */
function renderAnalyzeBody(ctx: DetailContext, container: HTMLElement): void {
  const { nodeState, nodeInfo, taskId, nodeId } = ctx;
  const instructions = nodeInfo?.instructions || '';
  const output = (typeof nodeState !== 'string' ? nodeState?.output : nodeState) || '';

  // Parse analytical evidence
  let evidence: EvidenceItem[] = [];
  if (typeof nodeState !== 'string' && nodeState?.analyticalEvidence) {
    try {
      evidence = JSON.parse(nodeState.analyticalEvidence);
    } catch { /* not JSON */ }
  }

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

  // Synthesis output (commentary)
  if (output) {
    html += `
      <details class="detail-section" open>
        <summary>Synthesis</summary>
        <div class="detail-section-content">${renderMarkdown(output)}</div>
      </details>
    `;
  }

  // Analytical Evidence — expanded by default (this IS the primary output)
  if (evidence.length > 0) {
    html += `
      <details class="detail-section" open>
        <summary>
          Analytical Evidence
          <span class="detail-section-meta">${evidence.length} ${evidence.length === 1 ? 'query' : 'queries'}</span>
        </summary>
        <div class="detail-section-content evidence-section">
          ${evidence.map(renderEvidenceCard).join('')}
        </div>
      </details>
    `;
  }

  // Thought Chain — reused from Probe, collapsed, lazy-loaded
  html += `
    <details class="detail-section" id="tc-section-analyze">
      <summary>
        Thought Chain
        <span class="detail-section-meta" id="tc-meta-analyze">loading…</span>
      </summary>
      <div class="detail-section-content" id="tc-container-analyze">
        <div class="tc-loading"><div class="spinner"></div> Loading steps…</div>
      </div>
    </details>
  `;

  container.innerHTML = html;

  // Wire lazy loading for the thought chain (same pattern as Probe)
  const tcSection = container.querySelector('#tc-section-analyze') as HTMLDetailsElement;
  if (tcSection) {
    let loaded = false;
    tcSection.addEventListener('toggle', () => {
      if (tcSection.open && !loaded) {
        loaded = true;
        const tcContainer = container.querySelector('#tc-container-analyze') as HTMLElement;
        if (tcContainer) {
          renderThoughtChainTimeline(tcContainer, taskId, nodeId);
        }
      }
    });
  }
}

/** Render a single evidence card with SQL + result table. */
function renderEvidenceCard(item: EvidenceItem): string {
  const sqlExcerpt = item.sql.length > 100 ? item.sql.substring(0, 100) + '…' : item.sql;
  const rowCount = item.rows?.length || 0;
  const totalRows = item.totalRows || rowCount;
  const rowsLabel = item.capped ? `${rowCount} of ${totalRows} rows` : `${totalRows} rows`;

  // Highlight SQL
  let highlightedSql: string;
  try {
    highlightedSql = hljs.highlight(item.sql, { language: 'sql' }).value;
  } catch {
    highlightedSql = escapeHtml(item.sql);
  }

  // Build result table
  let tableHtml = '';
  if (item.rows && item.rows.length > 0) {
    const columns = Object.keys(item.rows[0]);
    const headerCells = columns.map(c => `<th>${escapeHtml(c)}</th>`).join('');
    const bodyRows = item.rows.map(row => {
      const cells = columns.map(c => {
        const val = row[c];
        return `<td>${escapeHtml(String(val ?? ''))}</td>`;
      }).join('');
      return `<tr>${cells}</tr>`;
    }).join('');

    tableHtml = `
      <div class="evidence-table-wrapper">
        <table class="evidence-table">
          <thead><tr>${headerCells}</tr></thead>
          <tbody>${bodyRows}</tbody>
        </table>
      </div>
    `;

    if (item.capped) {
      tableHtml += `<div class="evidence-footer">Showing ${rowCount} of ${totalRows} rows</div>`;
    }
  }

  return `
    <details class="evidence-card" open>
      <summary>
        <span class="evidence-sql">${escapeHtml(sqlExcerpt)}</span>
        <span class="evidence-rows-meta">${rowsLabel}</span>
      </summary>
      <div class="evidence-body">
        <pre><code class="hljs language-sql">${highlightedSql}</code></pre>
        ${tableHtml}
      </div>
    </details>
  `;
}

// Register
registerRenderer('analyze', renderAnalyzeBody);
