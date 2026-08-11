// ============================================================
// api.ts — Daemon REST + SSE communication
// ============================================================

export interface TaskData {
  taskId: string;
  graph?: {
    nodes?: Array<{
      id: string;
      type?: string;
      action?: string;
      instructions?: string;
    }>;
    edges?: Array<{ sourceId: string; targetId: string }>;
  };
  states?: Record<string, any>;
  createdAt?: number;
  prompt?: string;
  intentType?: string;
  status?: string;
  nodeCount?: number;
}

/** Build the daemon base URL from the injected port or default. */
export function getDaemonUrl(): string {
  const port =
    (window as any).__TZRO_DAEMON_PORT__ || '8080';
  return `http://localhost:${port}`;
}

/** Fetch task data from the daemon REST API. */
export async function fetchTask(taskId: string): Promise<TaskData | null> {
  try {
    const resp = await fetch(`${getDaemonUrl()}/api/tasks?taskId=${taskId}`);
    if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
    const data = await resp.json();
    const tasks: TaskData[] = Array.isArray(data) ? data : [data];
    return tasks.find((t) => t.taskId === taskId) || tasks[0] || null;
  } catch (e) {
    console.error('Failed to fetch task:', e);
    return null;
  }
}

/** Connect to the SSE stream for live task updates. Returns cleanup function. */
export function connectSSE(
  taskId: string,
  onEvent: (chunk: any) => void,
  onError?: () => void,
): () => void {
  const url = `${getDaemonUrl()}/api/tasks/events?taskId=${taskId}`;
  let es: EventSource | null = new EventSource(url);
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  es.onmessage = (e) => {
    try {
      const chunk = JSON.parse(e.data);
      onEvent(chunk);
    } catch {
      /* ignore parse errors */
    }
  };

  es.onerror = () => {
    onError?.();
    // Reconnect after a brief delay
    reconnectTimer = setTimeout(() => {
      if (es) {
        es.close();
        es = new EventSource(url);
        es.onmessage = (e) => {
          try {
            onEvent(JSON.parse(e.data));
          } catch {
            /* ignore */
          }
        };
      }
    }, 3000);
  };

  // Cleanup function
  return () => {
    if (reconnectTimer) clearTimeout(reconnectTimer);
    if (es) {
      es.close();
      es = null;
    }
  };
}

/** Cancel a running task via the daemon API. */
export async function cancelTask(taskId: string): Promise<void> {
  try {
    await fetch(`${getDaemonUrl()}/api/tasks/cancel`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ taskId }),
    });
  } catch (e) {
    console.error('Cancel failed:', e);
  }
}

/** Discover the most recently created active task from the daemon. */
export async function discoverActiveTask(): Promise<string | null> {
  try {
    const resp = await fetch(`${getDaemonUrl()}/api/tasks`);
    if (!resp.ok) return null;
    const tasks: TaskData[] = await resp.json();
    const active = tasks
      .filter((t) => t.status === 'running' || t.status === 'pending')
      .sort((a, b) => (b.createdAt || 0) - (a.createdAt || 0));
    return active.length > 0 ? active[0].taskId! : null;
  } catch {
    return null;
  }
}

// ---- Thought Chain Steps ----

export interface ThoughtStep {
  stepIndex: number;
  thought: string;
  toolName?: string;
  toolArgs?: string;
  toolOutput?: string;
  truncated: boolean;
  createdAt: number;
}

/** Fetch thought chain steps for a Probe/Analyze node. */
export async function fetchNodeSteps(taskId: string, nodeId: string): Promise<ThoughtStep[]> {
  try {
    const resp = await fetch(`${getDaemonUrl()}/api/tasks/steps?taskId=${taskId}&nodeId=${nodeId}`);
    if (!resp.ok) return [];
    const data = await resp.json();
    return data.steps || [];
  } catch {
    return [];
  }
}

