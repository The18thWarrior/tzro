// ============================================================
// graph.ts — DAG utilities ported from progress.html
// ============================================================

/** Topologically sort graph nodes into parallel execution layers. */
export function topoLayers(
  nodes: Array<{ id: string }>,
  edges: Array<{ sourceId: string; targetId: string }>,
): string[][] {
  if (!nodes || !nodes.length) return [];

  const idSet = new Set(nodes.map((n) => n.id));
  const inDegree: Record<string, number> = {};
  const adj: Record<string, string[]> = {};

  nodes.forEach((n) => {
    inDegree[n.id] = 0;
    adj[n.id] = [];
  });

  (edges || []).forEach((e) => {
    if (idSet.has(e.sourceId) && idSet.has(e.targetId)) {
      inDegree[e.targetId] = (inDegree[e.targetId] || 0) + 1;
      adj[e.sourceId].push(e.targetId);
    }
  });

  const layers: string[][] = [];
  let frontier = nodes.filter((n) => inDegree[n.id] === 0).map((n) => n.id);
  const visited = new Set<string>();

  while (frontier.length > 0) {
    layers.push(frontier);
    const next: string[] = [];
    frontier.forEach((id) => {
      visited.add(id);
      (adj[id] || []).forEach((tid) => {
        inDegree[tid]--;
        if (inDegree[tid] === 0 && !visited.has(tid)) next.push(tid);
      });
    });
    frontier = next;
  }

  // Remaining nodes (cycles or disconnected)
  const remaining = nodes.filter((n) => !visited.has(n.id)).map((n) => n.id);
  if (remaining.length) layers.push(remaining);

  return layers;
}

/** Extract node status from the states map. */
export function nodeStatus(
  nodeId: string,
  states: Record<string, any> | null,
): string {
  if (!states) return 'pending';
  const s = states[nodeId];
  if (!s) return 'pending';
  return (typeof s === 'string' ? s : s.status) || 'pending';
}

/** Parse step progress from node state output (e.g. "Step 5/15: read_file"). */
export function nodeStepDetail(
  nodeId: string,
  states: Record<string, any> | null,
): { step: number; total: number; tool: string } | { synthesizing: true; findings: number } | null {
  if (!states) return null;
  const s = states[nodeId];
  if (!s || typeof s === 'string') return null;
  const output = s.output || '';
  const m = output.match(/^Step (\d+)\/(\d+): (.+)$/);
  if (m) return { step: parseInt(m[1]), total: parseInt(m[2]), tool: m[3] };
  const syn = output.match(/^Synthesizing \((\d+) findings\)$/);
  if (syn) return { synthesizing: true, findings: parseInt(syn[1]) };
  return null;
}

/** Format a duration in milliseconds to human-readable form. */
export function fmtDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${s % 60}s`;
}

/** Escape HTML special characters. */
export function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
