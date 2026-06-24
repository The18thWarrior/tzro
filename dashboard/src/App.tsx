import { useState, useEffect, useRef, useCallback } from 'react';
import { 
  Play, Loader2, RefreshCw, Moon, Sun, Layout, Activity, RotateCcw 
} from 'lucide-react';
import { renderLayoutWithFallback } from './adapter/json-to-openui';
import type { RenderContext } from './adapter/json-to-openui';

interface DashboardSpec {
  id: string;
  spec: {
    version: number;
    generatedAt: number;
    ttlSeconds: number;
    layout: any;
  };
  generatedAt: number;
  generatorTaskId: string;
  ttlSeconds: number;
}

export default function App() {
  // Theme Management
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    const saved = localStorage.getItem('theme');
    if (saved === 'light' || saved === 'dark') return saved;
    return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  });

  // Data states
  const [spec, setSpec] = useState<DashboardSpec | null>(null);
  const [loadingSpec, setLoadingSpec] = useState(true);
  const [regenerating, setRegenerating] = useState(false);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  // Regeneration progress tracking
  const regenTaskIdRef = useRef<string | null>(null);
  const [regenProgress, setRegenProgress] = useState(0);
  const [regenNodeLabel, setRegenNodeLabel] = useState('');
  const [regenComplete, setRegenComplete] = useState(false);
  const [regenFailed, setRegenFailed] = useState(false);
  const completedNodesRef = useRef<Set<string>>(new Set());

  const [tasks, setTasks] = useState<any[]>([]);
  const [events, setEvents] = useState<any[]>([]);
  const [config, setConfig] = useState<any>(null);
  const [sidecar, setSidecar] = useState<any>(null);
  const [downloadedModels, setDownloadedModels] = useState<any[]>([]);
  const [notifications, setNotifications] = useState<any[]>([]);
  const [workflows, setWorkflows] = useState<any[]>([]);
  const [workflowExecutions, setWorkflowExecutions] = useState<any[]>([]);
  
  // Spotlight state
  const [selectedTaskId, setSelectedTaskId] = useState<string | undefined>(undefined);
  const [selectedTaskDetails, setSelectedTaskDetails] = useState<any>(null);
  const selectedWorkflowExecution = null;
  const selectedWorkflowTasks: any[] = [];

  // Apply theme
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'light') {
      root.classList.add('light');
    } else {
      root.classList.remove('light');
    }
    localStorage.setItem('theme', theme);
  }, [theme]);

  // Fetch Dashboard Spec
  const fetchSpec = async () => {
    try {
      const res = await fetch('/api/dashboard/spec');
      if (res.status === 404) {
        setSpec(null);
        return;
      }
      if (!res.ok) throw new Error("Failed to fetch spec");
      const data = await res.json();
      setSpec(data);
      setErrorMsg(null);
    } catch (err: any) {
      console.error(err);
      setErrorMsg("Dashboard layout spec not found or server is offline.");
    } finally {
      setLoadingSpec(false);
    }
  };

  // Fetch all dashboard metrics & states
  const fetchTelemetry = async () => {
    try {
      // 1. Fetch tasks
      const tasksRes = await fetch('/api/tasks');
      if (tasksRes.ok) {
        const tasksData = await tasksRes.json();
        // Sort tasks descending by createdAt
        const sorted = (tasksData || []).sort((a: any, b: any) => b.createdAt - a.createdAt);
        setTasks(sorted);
      }

      // 2. Fetch config
      const configRes = await fetch('/api/config');
      if (configRes.ok) {
        const configData = await configRes.json();
        setConfig(configData);
      }

      // 3. Fetch sidecar
      const sidecarRes = await fetch('/api/sidecar');
      if (sidecarRes.ok) {
        const sidecarData = await sidecarRes.json();
        setSidecar(sidecarData);
      }

      // 4. Fetch models catalog for downloaded models
      const modelsRes = await fetch('/api/models');
      if (modelsRes.ok) {
        const modelsData = await modelsRes.json();
        const downloaded = (modelsData || []).filter((m: any) => m.downloaded);
        setDownloadedModels(downloaded);
      }

      // 5. Fetch Notifications (HIR approvals)
      const notifsRes = await fetch('/api/notifications');
      if (notifsRes.ok) {
        const notifsData = await notifsRes.json();
        setNotifications(notifsData || []);
      }

      // 6. Fetch Workflows
      const wfRes = await fetch('/api/workflows');
      if (wfRes.ok) {
        const wfData = await wfRes.json();
        setWorkflows(wfData || []);
      }

      // 7. Fetch Workflow Executions (run history)
      const wfExecRes = await fetch('/api/workflows/executions');
      if (wfExecRes.ok) {
        const wfExecData = await wfExecRes.json();
        setWorkflowExecutions(wfExecData || []);
      }
    } catch (err) {
      console.error("Telemetry fetch failed", err);
    }
  };

  // Fetch specific task details for Spotlight
  const fetchTaskDetails = async (taskId: string) => {
    try {
      const res = await fetch('/api/tasks');
      if (res.ok) {
        const list = await res.json();
        const found = list.find((t: any) => t.taskId === taskId);
        if (found) {
          setSelectedTaskDetails(found);
        }
      }
    } catch (err) {
      console.error(err);
    }
  };

  // Handle task selection
  const handleSelectTask = (taskId: string) => {
    setSelectedTaskId(taskId);
    fetchTaskDetails(taskId);
  };

  // Handle closing spotlight
  const handleCloseSpotlight = () => {
    setSelectedTaskId(undefined);
    setSelectedTaskDetails(null);
  };

  // Human-readable labels for dashboard generation nodes
  const nodeLabels: Record<string, string> = {
    'gather_metrics': 'Gathering system metrics',
    'gather_metrics_exec': 'Collecting metrics data',
    'gather_tasks': 'Gathering task history',
    'gather_tasks_exec': 'Collecting task data',
    'gather_config': 'Gathering configuration',
    'gather_config_exec': 'Collecting config data',
    'gather_workflows': 'Gathering workflows',
    'gather_workflows_exec': 'Collecting workflow data',
    'compose_layout': 'Composing layout',
    'compose_layout_exec': 'Generating layout spec',
  };
  // Total expected nodes for the dashboard generation DAG (5 base + 5 SCT exec = 10)
  const TOTAL_REGEN_NODES = 10;

  // SSE stream connection
  useEffect(() => {
    const sse = new EventSource('/api/events');
    
    sse.onmessage = (event) => {
      try {
        const chunk = JSON.parse(event.data);
        if (chunk && chunk.type) {
          setEvents((prev) => {
            const list = [...prev, {
              timestamp: new Date().toISOString(),
              eventType: chunk.type,
              taskId: chunk.taskId,
              nodeId: chunk.nodeId,
              payload: chunk.content || JSON.stringify(chunk),
            }];
            // Keep last 100 events
            if (list.length > 100) list.shift();
            return list;
          });

          // Track regeneration progress via SSE events
          if (regenTaskIdRef.current && chunk.taskId === regenTaskIdRef.current) {
            if (chunk.type === 'node_started' && chunk.nodeId) {
              const label = nodeLabels[chunk.nodeId] || chunk.nodeId;
              setRegenNodeLabel(label);
            }
            if (chunk.type === 'node_completed' && chunk.nodeId) {
              completedNodesRef.current.add(chunk.nodeId);
              const completed = completedNodesRef.current.size;
              const pct = Math.min(Math.round((completed / TOTAL_REGEN_NODES) * 100), 100);
              setRegenProgress(pct);
              const label = nodeLabels[chunk.nodeId] || chunk.nodeId;
              setRegenNodeLabel(`${label} ✓`);
            }
            if (chunk.type === 'task_completed') {
              setRegenProgress(100);
              setRegenComplete(true);
              setRegenerating(false);
              setRegenNodeLabel('Dashboard ready');
            }
            if (chunk.type === 'task_failed') {
              setRegenFailed(true);
              setRegenerating(false);
              setRegenNodeLabel('Generation failed');
            }
          }

          // Refresh spec or task status dynamically on critical events
          if (chunk.type === 'node_completed' && chunk.nodeId === 'compose_layout_exec') {
            // Don't auto-refresh spec during regen — let the reload button handle it
            if (!regenTaskIdRef.current || chunk.taskId !== regenTaskIdRef.current) {
              fetchSpec();
            }
          }
          if (chunk.type === 'node_completed' || chunk.type === 'node_started') {
            fetchTelemetry();
            if (selectedTaskId) {
              fetchTaskDetails(selectedTaskId);
            }
          }
        }
      } catch (err) {
        console.error(err);
      }
    };

    sse.onerror = (err) => {
      console.error("SSE connection error", err);
    };

    return () => {
      sse.close();
    };
  }, [selectedTaskId]);

  // Initial data fetch (SSE handles live updates — no polling needed)
  useEffect(() => {
    fetchSpec();
    fetchTelemetry();
  }, []);

  // Trigger Dashboard Regeneration (non-blocking, SSE-driven progress)
  const triggerRegenerate = async () => {
    // Reset regen state
    setRegenerating(true);
    setRegenProgress(0);
    setRegenNodeLabel('Initializing...');
    setRegenComplete(false);
    setRegenFailed(false);
    completedNodesRef.current = new Set();

    try {
      const res = await fetch('/api/dashboard/regenerate?wait=false', { method: 'POST' });
      if (!res.ok) throw new Error("Failed to trigger regeneration");
      const data = await res.json();
      regenTaskIdRef.current = data.taskId;
      // Progress is now tracked via SSE events in the useEffect above
    } catch (err) {
      console.error(err);
      setRegenerating(false);
      setRegenFailed(true);
      setRegenNodeLabel('Failed to start');
    }
  };

  // Reload page to show newly generated dashboard
  const handleReloadDashboard = useCallback(async () => {
    setRegenComplete(false);
    regenTaskIdRef.current = null;
    setRegenProgress(0);
    setRegenNodeLabel('');
    await fetchSpec();
  }, []);

  // Trigger arbitrary Workflow
  const handleTriggerWorkflow = async (wfId: string) => {
    try {
      const res = await fetch(`/api/workflows/trigger?id=${wfId}`, { method: 'POST' });
      if (res.ok) {
        fetchTelemetry();
      }
    } catch (err) {
      console.error(err);
    }
  };

  // Build Context for json-to-openui tree render
  const renderContext: RenderContext = {
    tasks,
    events,
    config,
    sidecar,
    downloadedModels: downloadedModels.map(m => m.id),
    notifications,
    workflows,
    workflowExecutions,
    selectedTaskId,
    onSelectTask: handleSelectTask,
    onCloseSpotlight: handleCloseSpotlight,
    selectedWorkflowExecution,
    selectedWorkflowTasks,
    onTriggerWorkflow: handleTriggerWorkflow,
    onRefreshNotifications: fetchTelemetry,
    selectedTaskDetails,
  };

  // Calculate staleness
  const specAgeSeconds = spec ? Math.floor(Date.now() / 1000) - spec.generatedAt : 0;
  const isStale = spec ? specAgeSeconds > spec.ttlSeconds : false;

  return (
    <div className="min-h-screen pb-12 transition-colors duration-300">
      {/* Header Bar */}
      <header className="sticky top-0 z-40 bg-[var(--bg-color)]/70 backdrop-filter backdrop-blur-md border-b border-[var(--glass-border)] transition-colors duration-300">
        <div className="max-w-7xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-indigo-500/10 rounded-lg text-indigo-400 border border-indigo-500/20 glow-accent">
              <Activity size={20} className="animate-pulse" />
            </div>
            <div>
              <h1 className="text-xl font-bold tracking-tight text-[var(--heading-color)] flex items-center gap-1.5">
                tzro <span className="text-xs font-mono text-indigo-400 font-semibold border border-indigo-500/20 px-1.5 py-0.5 rounded bg-indigo-500/5">Dashboard</span>
              </h1>
              <p className="text-[10px] text-[var(--muted-color)] tracking-wide">DURABLE AGENTIC OBSERVABILITY ENGINE</p>
            </div>
          </div>

          <div className="flex items-center gap-4">
            {/* Spec freshness indicator (hidden during regen) */}
            {spec && !regenerating && !regenComplete && (
              <div className="hidden md:flex items-center gap-2 text-xs">
                <span className={`w-2 h-2 rounded-full ${isStale ? 'bg-amber-400 animate-pulse' : 'bg-emerald-400'}`}></span>
                <span className="text-[var(--muted-color)]">
                  Spec: {isStale ? 'Stale' : 'Fresh'} (Age: {Math.round(specAgeSeconds / 60)}m / TTL: {Math.round(spec.ttlSeconds / 60)}m)
                </span>
              </div>
            )}

            {/* Progress bar during regeneration */}
            {(regenerating || regenComplete) && (
              <div className="hidden md:flex items-center gap-3 min-w-[280px]">
                <div className="flex-1">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-[10px] font-semibold text-[var(--muted-color)] uppercase tracking-wider">
                      {regenComplete ? 'Generation complete' : regenFailed ? 'Generation failed' : 'Regenerating'}
                    </span>
                    <span className="text-[10px] font-mono text-indigo-400">{regenProgress}%</span>
                  </div>
                  <div className="h-1.5 bg-white/5 rounded-full overflow-hidden border border-[var(--glass-border)]">
                    <div 
                      className={`h-full rounded-full transition-all duration-500 ease-out ${
                        regenComplete 
                          ? 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.4)]'
                          : regenFailed
                            ? 'bg-rose-500 shadow-[0_0_8px_rgba(239,68,68,0.4)]'
                            : 'bg-gradient-to-r from-indigo-500 via-violet-500 to-indigo-500 shadow-[0_0_8px_rgba(99,102,241,0.4)] regen-progress-shimmer'
                      }`}
                      style={{ width: `${regenProgress}%` }}
                    />
                  </div>
                  <p className="text-[9px] text-[var(--muted-color)] mt-0.5 truncate max-w-[260px]">
                    {regenNodeLabel}
                  </p>
                </div>
              </div>
            )}

            {/* Reload button appears when regeneration completes */}
            {regenComplete ? (
              <button
                onClick={handleReloadDashboard}
                className="px-4 py-2 bg-emerald-500 hover:bg-emerald-600 text-white rounded-lg text-xs font-bold flex items-center gap-2 transition-all cursor-pointer glow-success regen-reload-pulse"
              >
                <RotateCcw size={14} /> Reload Dashboard
              </button>
            ) : (
              <button 
                onClick={triggerRegenerate}
                disabled={regenerating}
                className="px-4 py-2 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded-lg text-xs font-bold flex items-center gap-2 transition-all cursor-pointer glow-accent"
              >
                {regenerating ? (
                  <>
                    <Loader2 size={14} className="animate-spin" /> Regenerating...
                  </>
                ) : (
                  <>
                    <RefreshCw size={14} /> Regenerate Layout
                  </>
                )}
              </button>
            )}

            <button 
              onClick={() => setTheme(prev => prev === 'dark' ? 'light' : 'dark')}
              className="p-2 bg-white/5 hover:bg-white/10 rounded-lg text-[var(--fg-color)] transition-all cursor-pointer border border-[var(--glass-border)]"
            >
              {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
            </button>
          </div>
        </div>
      </header>

      {/* Main Content Space */}
      <main className="max-w-7xl mx-auto px-6 mt-8">
        {errorMsg && (
          <div className="mb-6 p-4 border border-rose-500 bg-rose-500/10 text-rose-300 rounded-lg">
            {errorMsg}
          </div>
        )}
        {loadingSpec ? (
          <div className="flex flex-col items-center justify-center h-80 text-[var(--muted-color)]">
            <Loader2 className="animate-spin mb-3 text-indigo-400" size={32} />
            <p className="text-sm font-semibold">Loading agentic spec layout...</p>
          </div>
        ) : spec ? (
          <div className="space-y-6">
            {/* Dynamic UI Render mapping tree */}
            {renderLayoutWithFallback(spec.spec?.layout, renderContext)}
          </div>
        ) : (
          <div className="glass-panel p-12 text-center max-w-2xl mx-auto mt-12 border border-indigo-500/10">
            <div className="p-4 bg-indigo-500/10 rounded-full text-indigo-400 inline-block mb-4 border border-indigo-500/20 glow-accent">
              <Layout size={40} />
            </div>
            <h2 className="text-2xl font-bold text-[var(--heading-color)] tracking-tight">No Observability Dashboard Compiled</h2>
            <p className="text-sm text-[var(--muted-color)] mt-3 leading-relaxed">
              The agentic dashboard layout specification has not been built yet. 
              The Strategic Planner compiles system state, telemetry statistics, sidecar processes, 
              and GGBF models into a curated layout tree to best fit the current status of the engine.
            </p>
            <div className="mt-8">
              <button 
                onClick={triggerRegenerate}
                disabled={regenerating}
                className="px-6 py-3 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded-lg text-sm font-bold inline-flex items-center gap-2 transition-all cursor-pointer glow-accent"
              >
                {regenerating ? (
                  <>
                    <Loader2 size={16} className="animate-spin" /> Generating Observability Dashboard...
                  </>
                ) : (
                  <>
                    <Play size={16} /> Build Agentic Observability Dashboard
                  </>
                )}
              </button>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}
