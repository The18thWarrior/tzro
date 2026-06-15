import { useState, useEffect } from 'react';
import { 
  Play, Loader2, RefreshCw, Moon, Sun, Layout, Activity 
} from 'lucide-react';
import { renderLayoutNode } from './adapter/json-to-openui';
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

  const [tasks, setTasks] = useState<any[]>([]);
  const [events, setEvents] = useState<any[]>([]);
  const [config, setConfig] = useState<any>(null);
  const [sidecar, setSidecar] = useState<any>(null);
  const [downloadedModels, setDownloadedModels] = useState<any[]>([]);
  const [notifications, setNotifications] = useState<any[]>([]);
  const [workflows, setWorkflows] = useState<any[]>([]);
  
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

          // Refresh spec or task status dynamically on critical events
          if (chunk.type === 'node_completed' && chunk.nodeId === 'terminal_synthesis_tool_exec') {
            fetchSpec();
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

  // Initial and polling data fetch
  useEffect(() => {
    fetchSpec();
    fetchTelemetry();

    const interval = setInterval(() => {
      fetchTelemetry();
      if (selectedTaskId) {
        fetchTaskDetails(selectedTaskId);
      }
    }, 5000);

    return () => clearInterval(interval);
  }, [selectedTaskId]);

  // Trigger Dashboard Regeneration
  const triggerRegenerate = async () => {
    setRegenerating(true);
    try {
      const res = await fetch('/api/dashboard/regenerate?wait=true', { method: 'POST' });
      if (!res.ok) throw new Error("Failed to trigger regeneration");
      const data = await res.json();
      if (data.status === 'completed') {
        await fetchSpec();
      } else {
        // Fallback polling for status
        let attempts = 0;
        const checkInterval = setInterval(async () => {
          attempts++;
          const statusRes = await fetch(`/api/tasks`);
          if (statusRes.ok) {
            const list = await statusRes.json();
            const genTask = list.find((t: any) => t.taskId === data.taskId);
            const isDone = genTask && Object.values(genTask.states || {}).some(
              (s: any) => s.nodeId === 'terminal_synthesis_tool' && s.status === 'completed'
            );
            if (isDone || attempts > 12) {
              clearInterval(checkInterval);
              await fetchSpec();
              setRegenerating(false);
            }
          }
        }, 2500);
        return;
      }
    } catch (err) {
      console.error(err);
    } finally {
      setRegenerating(false);
    }
  };

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
    selectedTaskId,
    onSelectTask: handleSelectTask,
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
              <h1 className="text-xl font-bold tracking-tight text-white flex items-center gap-1.5">
                tzro <span className="text-xs font-mono text-indigo-400 font-semibold border border-indigo-500/20 px-1.5 py-0.5 rounded bg-indigo-500/5">Dashboard</span>
              </h1>
              <p className="text-[10px] text-[var(--muted-color)] tracking-wide">DURABLE AGENTIC OBSERVABILITY ENGINE</p>
            </div>
          </div>

          <div className="flex items-center gap-4">
            {spec && (
              <div className="hidden md:flex items-center gap-2 text-xs">
                <span className={`w-2 h-2 rounded-full ${isStale ? 'bg-amber-400 animate-pulse' : 'bg-emerald-400'}`}></span>
                <span className="text-[var(--muted-color)]">
                  Spec: {isStale ? 'Stale' : 'Fresh'} (Age: {Math.round(specAgeSeconds / 60)}m / TTL: {Math.round(spec.ttlSeconds / 60)}m)
                </span>
              </div>
            )}

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

            <button 
              onClick={() => setTheme(prev => prev === 'dark' ? 'light' : 'dark')}
              className="p-2 bg-white/5 hover:bg-white/10 rounded-lg text-slate-300 transition-all cursor-pointer border border-[var(--glass-border)]"
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
            {renderLayoutNode(spec.spec?.layout, renderContext)}
          </div>
        ) : (
          <div className="glass-panel p-12 text-center max-w-2xl mx-auto mt-12 border border-indigo-500/10">
            <div className="p-4 bg-indigo-500/10 rounded-full text-indigo-400 inline-block mb-4 border border-indigo-500/20 glow-accent">
              <Layout size={40} />
            </div>
            <h2 className="text-2xl font-bold text-white tracking-tight">No Observability Dashboard Compiled</h2>
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
