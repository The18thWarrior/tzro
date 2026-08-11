// ============================================================
// main.ts — App entry point
// SDK init, task acquisition, view switching, data loop
// ============================================================

import './style.css';
import {
  App,
  applyDocumentTheme,
  applyHostStyleVariables,
  applyHostFonts,
} from '@modelcontextprotocol/ext-apps';
import { appState } from './state';
import { fetchTask, connectSSE, discoverActiveTask } from './api';
import { renderOverview } from './views/overview';
import { renderDetailView } from './views/detail-router';
// Phase 2 renderers — side-effect imports trigger registerRenderer()
import './views/detail-probe';
import './views/detail-code';
import './views/detail-synthesis';
import './views/detail-analyze';

// ---- DOM Setup ----
const appEl = document.getElementById('app')!;

// Create view containers
const overviewEl = document.createElement('div');
overviewEl.id = 'overview-view';
overviewEl.className = 'view view-visible';

const detailEl = document.createElement('div');
detailEl.id = 'detail-view';
detailEl.className = 'view view-hidden';

// ---- State → Render wiring ----
appState.subscribe((state) => {
  if (state.currentView === 'overview') {
    overviewEl.className = 'view view-visible';
    detailEl.className = 'view view-hidden';
    if (state.taskData) renderOverview(overviewEl, state);
  } else {
    overviewEl.className = 'view view-hidden';
    detailEl.className = 'view view-visible';
    if (state.taskData) renderDetailView(detailEl, state);
  }
});

// ---- Task ID Acquisition ----

/** Extract taskId from the SDK tool result content. */
function extractTaskIdFromToolResult(content: any[]): { taskId: string; daemonPort?: string } | null {
  for (const c of content || []) {
    if (c.type === 'text' && c.text) {
      try {
        const parsed = JSON.parse(c.text);
        if (parsed.taskId) return parsed;
      } catch { /* not JSON */ }
    }
  }
  return null;
}

/** Try all sources for the taskId in priority order. */
function getTaskId(): string | null {
  // 1. URL params (dev/testing)
  const params = new URLSearchParams(window.location.search);
  const fromUrl = params.get('taskId');
  if (fromUrl) return fromUrl;

  // 2. Go-injected global (fallback for hosts without SDK support)
  if ((window as any).__TZRO_TASK_ID__) return (window as any).__TZRO_TASK_ID__;

  return null;
}

// ---- SDK Integration ----
let sdkApp: App | null = null;

function initSdk(): void {
  try {
    sdkApp = new App(
      { name: 'tzro-progress', version: '1.0.0' },
      {}, // no app capabilities needed
    );

    // Theme integration — register before connect to avoid missing one-shot events
    sdkApp.onhostcontextchanged = (ctx) => {
      if (ctx.theme) applyDocumentTheme(ctx.theme);
      if (ctx.styles?.variables) applyHostStyleVariables(ctx.styles.variables);
      if (ctx.styles?.css?.fonts) applyHostFonts(ctx.styles.css.fonts);
    };

    // Tool result delivery — primary path for taskId acquisition
    sdkApp.ontoolresult = (params) => {
      const content = params.content || [];
      const extracted = extractTaskIdFromToolResult(content);
      if (extracted) {
        if (extracted.daemonPort) {
          (window as any).__TZRO_DAEMON_PORT__ = extracted.daemonPort;
        }
        startWithTaskId(extracted.taskId);
      }
    };

    sdkApp.connect().catch((err: unknown) => {
      console.warn('SDK connect failed (expected in non-host environments):', err);
    });
  } catch (err) {
    console.warn('SDK unavailable:', err);
  }
}

// ---- Data Loop ----
let cleanupSSE: (() => void) | null = null;
let pollInterval: ReturnType<typeof setInterval> | null = null;

async function startWithTaskId(taskId: string): Promise<void> {
  // Prevent re-init if already running with this taskId
  if (appState.taskId === taskId) return;

  appState.setTaskId(taskId);

  // Show loading state
  appEl.innerHTML = '';
  appEl.appendChild(overviewEl);
  appEl.appendChild(detailEl);
  overviewEl.innerHTML = `<div class="loading"><div class="spinner"></div><p>Connecting to task…</p></div>`;

  // Poll until task has nodes or reaches terminal state
  let found = false;
  for (let i = 0; i < 30; i++) {
    const task = await fetchTask(taskId);
    if (task) {
      const nodes = task.graph?.nodes || [];
      const status = task.status;
      if (nodes.length > 0 || status === 'completed' || status === 'failed') {
        appState.setTaskData(task);
        found = true;
        break;
      }
    }
    await sleep(1000);
  }

  if (!found) {
    overviewEl.innerHTML = renderAccepted(taskId);
  }

  // Start live updates regardless — task may appear or update later
  cleanupSSE = connectSSE(taskId, async (chunk) => {
    const task = await fetchTask(taskId);
    if (task) appState.setTaskData(task);

    // Stop on terminal events
    if (chunk.type === 'task_completed' || chunk.type === 'task_failed' || chunk.type === 'task_cancelled') {
      stopLiveUpdates();
    }
  });

  // Backup polling
  pollInterval = setInterval(async () => {
    if (appState.isTerminal) {
      stopLiveUpdates();
      return;
    }
    const task = await fetchTask(taskId);
    if (task) appState.setTaskData(task);
  }, 2000);
}

function stopLiveUpdates(): void {
  if (cleanupSSE) { cleanupSSE(); cleanupSSE = null; }
  if (pollInterval) { clearInterval(pollInterval); pollInterval = null; }
}

function renderAccepted(taskId: string): string {
  const shortId = taskId.substring(0, 8);
  return `
    <div class="header">
      <div class="header-left">
        <span class="logo">tzro</span>
        <span class="task-id">${shortId}…</span>
        <span class="status-badge status-running">accepted</span>
      </div>
    </div>
    <div class="loading">
      <p>Task accepted — executing in background</p>
      <p style="font-size:11px;color:var(--text-muted)">Use <code>tzro_status</code> to check progress</p>
    </div>
  `;
}

// ---- Discovery Loop ----
async function discoverLoop(): Promise<void> {
  for (let i = 0; i < 30; i++) {
    // Check if SDK delivered the taskId while we were polling
    if (appState.taskId) return;

    const taskId = await discoverActiveTask();
    if (taskId) {
      startWithTaskId(taskId);
      return;
    }
    await sleep(2000);
  }
  appEl.innerHTML = '<div class="error-msg">Could not discover active task. Check that the daemon is running.</div>';
}

// ---- Init ----
async function init(): Promise<void> {
  // Initialize SDK (non-blocking)
  initSdk();

  // Check for immediate taskId
  const taskId = getTaskId();
  if (taskId) {
    startWithTaskId(taskId);
  } else {
    // No taskId yet — start discovery loop
    discoverLoop();
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

init();
