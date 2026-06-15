import React, { useState, useEffect, useRef, useLayoutEffect } from 'react';
import { 
  Play, CheckCircle, XCircle, Loader2, Cpu, Shield, Layers, 
  Bell, Zap, Clock, CornerDownRight, Eye, HelpCircle, AlertTriangle
} from 'lucide-react';

// ==========================================
// HELPERS
// ==========================================

export const StatusBadge: React.FC<{ status: string }> = ({ status }) => {
  const baseClass = "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-semibold uppercase tracking-wider";
  switch (status.toLowerCase()) {
    case 'completed':
    case 'success':
      return <span className={`${baseClass} bg-emerald-500/10 text-emerald-400 border border-emerald-500/20 glow-success`}><CheckCircle size={12} /> Success</span>;
    case 'failed':
    case 'error':
      return <span className={`${baseClass} bg-rose-500/10 text-rose-400 border border-rose-500/20 glow-danger`}><XCircle size={12} /> Failed</span>;
    case 'running':
    case 'generating':
      return <span className={`${baseClass} bg-indigo-500/10 text-indigo-400 border border-indigo-500/20 glow-accent`}><Loader2 className="animate-spin" size={12} /> Running</span>;
    case 'waiting_for_client':
    case 'paused':
      return <span className={`${baseClass} bg-amber-500/10 text-amber-400 border border-amber-500/20 glow-warning`}><Clock size={12} /> Paused</span>;
    case 'skipped':
      return <span className={`${baseClass} bg-slate-500/10 text-slate-400 border border-slate-500/20`}>Skipped</span>;
    default:
      return <span className={`${baseClass} bg-slate-500/10 text-slate-400 border border-slate-500/20`}>{status}</span>;
  }
};

// ==========================================
// 1. LAYOUT COMPONENTS
// ==========================================

export const Stack: React.FC<{ children: React.ReactNode; gap?: string }> = ({ children, gap = 'gap-4' }) => {
  return <div className={`flex flex-col ${gap}`}>{children}</div>;
};

export const Grid: React.FC<{ children: React.ReactNode; columns?: number }> = ({ children, columns = 3 }) => {
  const gridCols = {
    1: 'grid-cols-1',
    2: 'grid-cols-1 md:grid-cols-2',
    3: 'grid-cols-1 md:grid-cols-2 lg:grid-cols-3',
    4: 'grid-cols-1 md:grid-cols-2 lg:grid-cols-4',
  }[columns] || 'grid-cols-1 md:grid-cols-3';

  return <div className={`grid ${gridCols} gap-4`}>{children}</div>;
};

export const Section: React.FC<{ title: string; children: React.ReactNode; subtitle?: string }> = ({ title, children, subtitle }) => {
  return (
    <div className="glass-panel p-6">
      <div className="border-b border-[var(--glass-border)] pb-4 mb-4">
        <h2 className="text-xl font-bold tracking-tight text-white flex items-center gap-2">{title}</h2>
        {subtitle && <p className="text-xs text-[var(--muted-color)] mt-1">{subtitle}</p>}
      </div>
      <div>{children}</div>
    </div>
  );
};

// ==========================================
// 2. STATIC LEAF COMPONENTS
// ==========================================

export const MetricCard: React.FC<{
  label: string;
  value: string;
  trend?: 'up' | 'down' | 'stable';
  trendValue?: string;
}> = ({ label, value, trend, trendValue }) => {
  return (
    <div className="glass-panel glass-panel-hover p-5 flex flex-col justify-between h-32 relative overflow-hidden">
      <div className="text-sm font-medium text-[var(--muted-color)]">{label}</div>
      <div className="text-3xl font-bold text-white tracking-tight mt-1">{value}</div>
      {trend && (
        <div className="absolute bottom-5 right-5">
          <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-semibold ${
            trend === 'up' ? 'bg-emerald-500/10 text-emerald-400' :
            trend === 'down' ? 'bg-rose-500/10 text-rose-400' : 'bg-slate-500/10 text-slate-400'
          }`}>
            {trend === 'up' ? '↑' : trend === 'down' ? '↓' : '•'} {trendValue || trend}
          </span>
        </div>
      )}
    </div>
  );
};

export const ConfigPanel: React.FC<{
  config?: {
    modelMode: string;
    sidecarEnabled: boolean;
    activeModel: string;
    privacyLevel: string;
  };
  downloadedModels?: string[];
}> = ({ config, downloadedModels = [] }) => {
  if (!config) return <div className="text-sm text-[var(--muted-color)]">No configuration data loaded.</div>;

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
      <div className="glass-panel p-4 flex items-start gap-4">
        <div className="p-3 bg-indigo-500/10 text-indigo-400 rounded-lg"><Cpu size={20} /></div>
        <div>
          <h4 className="text-sm font-semibold text-white">Model Mode & Active Model</h4>
          <p className="text-xs text-[var(--muted-color)] mt-0.5 capitalize">{config.modelMode} Planning Mode</p>
          <div className="text-xs font-mono bg-black/30 text-indigo-300 px-2 py-1 rounded mt-2 border border-white/5 break-all">
            {config.activeModel || "No model configured"}
          </div>
        </div>
      </div>
      <div className="glass-panel p-4 flex items-start gap-4">
        <div className="p-3 bg-emerald-500/10 text-emerald-400 rounded-lg"><Shield size={20} /></div>
        <div>
          <h4 className="text-sm font-semibold text-white">Privacy & Engine Settings</h4>
          <p className="text-xs text-[var(--muted-color)] mt-0.5">Level: {config.privacyLevel}</p>
          <div className="flex gap-2 mt-2">
            <span className={`px-2 py-0.5 rounded text-[10px] font-semibold border ${
              config.sidecarEnabled ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' : 'bg-rose-500/10 text-rose-400 border-rose-500/20'
            }`}>
              Sidecar: {config.sidecarEnabled ? 'Enabled' : 'Disabled'}
            </span>
            <span className="px-2 py-0.5 rounded text-[10px] font-semibold bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
              Downloaded: {downloadedModels.length}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
};

export const SidecarStatus: React.FC<{
  sidecar?: {
    status: string;
    activePort: number;
    activePid: number;
    manifestProgress: number;
    ggufModelPath: string;
  };
}> = ({ sidecar }) => {
  if (!sidecar) return <div className="text-sm text-[var(--muted-color)]">Sidecar status unknown.</div>;

  const isDownloading = sidecar.status === "Downloading" && sidecar.manifestProgress < 100;

  return (
    <div className="glass-panel p-5">
      <div className="flex justify-between items-start">
        <div>
          <h3 className="text-lg font-bold text-white flex items-center gap-2">Local Sidecar Node</h3>
          <p className="text-xs text-[var(--muted-color)] font-mono mt-1">
            PID: {sidecar.activePid || "N/A"} | Port: {sidecar.activePort || "N/A"}
          </p>
        </div>
        <StatusBadge status={sidecar.status} />
      </div>

      {isDownloading && (
        <div className="mt-4">
          <div className="flex justify-between text-xs font-semibold mb-1 text-indigo-400">
            <span>Downloading GGUF Model...</span>
            <span>{sidecar.manifestProgress}%</span>
          </div>
          <div className="w-full bg-white/5 rounded-full h-1.5 overflow-hidden border border-white/5">
            <div className="bg-indigo-500 h-full transition-all duration-300" style={{ width: `${sidecar.manifestProgress}%` }}></div>
          </div>
        </div>
      )}

      {sidecar.ggufModelPath && (
        <div className="mt-4">
          <div className="text-xs text-[var(--muted-color)]">Active model file path:</div>
          <div className="text-xs font-mono bg-black/25 text-indigo-300 px-2.5 py-1.5 rounded border border-white/5 break-all mt-1">
            {sidecar.ggufModelPath}
          </div>
        </div>
      )}
    </div>
  );
};

export const Annotation: React.FC<{
  type?: 'info' | 'warning' | 'tip';
  message: string;
}> = ({ type = 'info', message }) => {
  const classes = {
    info: 'bg-indigo-500/5 text-indigo-300 border-indigo-500/20 hover:border-indigo-500/40',
    warning: 'bg-amber-500/5 text-amber-300 border-amber-500/20 hover:border-amber-500/40',
    tip: 'bg-emerald-500/5 text-emerald-300 border-emerald-500/20 hover:border-emerald-500/40',
  }[type];

  const icons = {
    info: <AlertTriangle size={16} className="text-indigo-400 flex-shrink-0" />,
    warning: <AlertTriangle size={16} className="text-amber-400 flex-shrink-0" />,
    tip: <Zap size={16} className="text-emerald-400 flex-shrink-0" />,
  }[type];

  return (
    <div className={`glass-panel p-4 border flex items-start gap-3 text-sm leading-relaxed transition-all ${classes}`}>
      {icons}
      <div className="flex-1">{message}</div>
    </div>
  );
};

// ==========================================
// 3. LIVE INTERACTIVE LEAF COMPONENTS
// ==========================================

export const TaskTable: React.FC<{
  tasks: Array<{
    taskId: string;
    prompt: string;
    intentType: string;
    status: string;
    completedAt: number;
    createdAt?: number;
  }>;
  onSelectTask: (taskId: string) => void;
  selectedTaskId?: string;
}> = ({ tasks = [], onSelectTask, selectedTaskId }) => {
  if (tasks.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No recent tasks recorded in this window.</div>;
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-[var(--glass-border)] text-xs font-semibold uppercase tracking-wider text-[var(--muted-color)]">
            <th className="py-3 px-4">Task ID</th>
            <th className="py-3 px-4">Prompt</th>
            <th className="py-3 px-4">Intent</th>
            <th className="py-3 px-4">Status</th>
            <th className="py-3 px-4 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((task) => (
            <tr 
              key={task.taskId} 
              className={`border-b border-[var(--glass-border)] text-sm transition-colors hover:bg-white/2 cursor-pointer ${
                selectedTaskId === task.taskId ? 'bg-indigo-500/5 border-l-2 border-l-indigo-500' : ''
              }`}
              onClick={() => onSelectTask(task.taskId)}
            >
              <td className="py-3.5 px-4 font-mono text-xs text-indigo-300">{task.taskId.substring(0, 13)}...</td>
              <td className="py-3.5 px-4 max-w-[200px] truncate text-white" title={task.prompt}>{task.prompt}</td>
              <td className="py-3.5 px-4 capitalize text-[var(--muted-color)]">{task.intentType || "Task"}</td>
              <td className="py-3.5 px-4"><StatusBadge status={task.status} /></td>
              <td className="py-3.5 px-4 text-right">
                <button 
                  onClick={(e) => { e.stopPropagation(); onSelectTask(task.taskId); }}
                  className="inline-flex items-center gap-1 text-xs font-semibold text-indigo-400 hover:text-indigo-300"
                >
                  <Eye size={14} /> Spotlight
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

export const EventFeed: React.FC<{
  events: Array<{
    id?: string;
    timestamp: string;
    eventType: string;
    taskId?: string;
    nodeId?: string;
    payload: string;
  }>;
}> = ({ events = [] }) => {
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [events]);

  if (events.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6 font-mono">Connecting to SSE Telemetry stream...</div>;
  }

  return (
    <div ref={scrollRef} className="h-60 overflow-y-auto font-mono text-xs leading-relaxed space-y-2.5 pr-2 bg-black/20 p-4 rounded-lg border border-[var(--glass-border)]">
      {events.map((ev, idx) => {
        const timeStr = new Date(ev.timestamp).toLocaleTimeString();
        let payloadParsed = ev.payload;
        try {
          // If JSON object, pretty print it
          const parsed = JSON.parse(ev.payload);
          payloadParsed = JSON.stringify(parsed);
        } catch { }

        let color = "text-[var(--muted-color)]";
        if (ev.eventType.includes("fail") || ev.eventType.includes("error")) {
          color = "text-rose-400";
        } else if (ev.eventType.includes("complete") || ev.eventType.includes("success")) {
          color = "text-emerald-400";
        } else if (ev.eventType.includes("start") || ev.eventType.includes("running")) {
          color = "text-indigo-400";
        } else if (ev.eventType.includes("pause") || ev.eventType.includes("waiting")) {
          color = "text-amber-400";
        }

        return (
          <div key={idx} className="flex items-start gap-2 border-b border-white/2 pb-1.5 last:border-0 last:pb-0">
            <span className="text-slate-500 flex-shrink-0">{timeStr}</span>
            <span className={`font-semibold uppercase flex-shrink-0 ${color}`}>[{ev.eventType}]</span>
            {ev.nodeId && <span className="text-indigo-300 font-semibold flex-shrink-0">{ev.nodeId}:</span>}
            <span className="text-slate-300 break-all">{payloadParsed}</span>
          </div>
        );
      })}
    </div>
  );
};

export const NotificationList: React.FC<{
  notifications: Array<{
    id: string;
    source: string;
    type: string;
    title: string;
    message: string;
    taskId?: string;
    workflowId?: string;
    targetId?: string;
    status: string;
    createdAt: number;
  }>;
  onRefresh: () => void;
}> = ({ notifications = [], onRefresh }) => {
  const [actioning, setActioning] = useState<string | null>(null);

  const handleAction = async (id: string, approve: boolean, value?: string) => {
    setActioning(id);
    try {
      const targetNotif = notifications.find(n => n.id === id);
      if (!targetNotif) return;

      if (targetNotif.type === "approval_request") {
        // Actually, let's call the hook approve API or client tool submit API based on the type
        const res = await fetch(`/api/mcp`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            method: 'tzro_hook_approve',
            arguments: {
              taskId: targetNotif.taskId,
              nodeId: targetNotif.targetId,
              approve: approve
            }
          })
        });
        if (res.ok) {
          // Update status in local DB
          await fetch(`/api/notifications/update`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: id, status: 'read' })
          });
        }
      } else if (targetNotif.type === "client_tool_request") {
        const res = await fetch(`/api/mcp`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            method: 'tzro_client_tool_submit',
            arguments: {
              taskId: targetNotif.taskId,
              toolName: targetNotif.title,
              output: value || "Submitting mock action"
            }
          })
        });
        if (res.ok) {
          await fetch(`/api/notifications/update`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: id, status: 'read' })
          });
        }
      }
      onRefresh();
    } catch (err) {
      console.error(err);
    } finally {
      setActioning(null);
    }
  };

  const activeNotifs = notifications.filter(n => n.status === "unread");

  if (activeNotifs.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No pending notifications or approval requests.</div>;
  }

  return (
    <div className="space-y-3">
      {activeNotifs.map((n) => {
        const isApproval = n.type === "approval_request";
        const isClientTool = n.type === "client_tool_request";

        return (
          <div key={n.id} className="glass-panel p-4 border border-indigo-500/10 flex flex-col md:flex-row md:items-center justify-between gap-4">
            <div className="flex items-start gap-3">
              <div className={`p-2 rounded-lg mt-0.5 ${isApproval ? 'bg-amber-500/10 text-amber-400' : 'bg-indigo-500/10 text-indigo-400'}`}>
                {isApproval ? <Clock size={16} /> : <Bell size={16} />}
              </div>
              <div>
                <h4 className="text-sm font-semibold text-white">{n.title}</h4>
                <p className="text-xs text-slate-300 mt-1">{n.message}</p>
                {n.taskId && <p className="text-[10px] font-mono text-indigo-300 mt-1">Task ID: {n.taskId.substring(0, 10)}...</p>}
              </div>
            </div>

            {(isApproval || isClientTool) && (
              <div className="flex gap-2 justify-end">
                {isApproval ? (
                  <>
                    <button 
                      disabled={actioning === n.id}
                      onClick={() => handleAction(n.id, true)}
                      className="px-3 py-1.5 bg-emerald-500 hover:bg-emerald-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                    >
                      {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <CheckCircle size={12} />} Approve
                    </button>
                    <button 
                      disabled={actioning === n.id}
                      onClick={() => handleAction(n.id, false)}
                      className="px-3 py-1.5 bg-rose-500 hover:bg-rose-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                    >
                      {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <XCircle size={12} />} Reject
                    </button>
                  </>
                ) : (
                  <button 
                    disabled={actioning === n.id}
                    onClick={() => {
                      const val = prompt("Enter results output value for client tool execution:");
                      if (val !== null) handleAction(n.id, true, val);
                    }}
                    className="px-3 py-1.5 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                  >
                    {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />} Submit Result
                  </button>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};

export const WorkflowMonitor: React.FC<{
  workflows: Array<{
    id: string;
    name: string;
    description: string;
    triggerType: string;
    triggerConfig: string;
    status: string;
    nextRunAt: number;
    tasks?: any[];
  }>;
  onTrigger: (wfId: string) => void;
}> = ({ workflows = [], onTrigger }) => {
  const [triggering, setTriggering] = useState<string | null>(null);

  const handleRun = async (wfId: string) => {
    setTriggering(wfId);
    try {
      await onTrigger(wfId);
    } finally {
      setTriggering(wfId);
      setTimeout(() => setTriggering(null), 1000);
    }
  };

  if (workflows.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No workflows currently registered.</div>;
  }

  return (
    <div className="space-y-4">
      {workflows.map((wf) => (
        <div key={wf.id} className="glass-panel p-4 flex flex-col md:flex-row md:items-center justify-between gap-4 relative overflow-hidden">
          <div className="flex-1">
            <div className="flex items-center gap-3">
              <h4 className="text-base font-bold text-white">{wf.name}</h4>
              <span className={`px-2 py-0.5 rounded text-[10px] font-semibold border ${
                wf.status === 'active' ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' : 'bg-slate-500/10 text-slate-400 border-slate-500/20'
              }`}>
                {wf.status}
              </span>
            </div>
            <p className="text-xs text-[var(--muted-color)] mt-1">{wf.description}</p>
            
            <div className="flex flex-wrap gap-x-6 gap-y-2 mt-3 text-xs text-slate-400">
              <div>
                <span className="font-semibold text-slate-300">Trigger:</span> {wf.triggerType} {wf.triggerConfig ? `(${wf.triggerConfig})` : ''}
              </div>
              {wf.nextRunAt > 0 && (
                <div>
                  <span className="font-semibold text-slate-300">Next Run:</span> {new Date(wf.nextRunAt * 1000).toLocaleString()}
                </div>
              )}
            </div>

            {wf.tasks && wf.tasks.length > 0 && (
              <div className="mt-3.5 pt-3 border-t border-white/5">
                <div className="text-xs font-semibold text-white mb-2 flex items-center gap-1"><Layers size={12} /> Configured Task Steps:</div>
                <div className="space-y-1.5 pl-3 border-l border-indigo-500/20">
                  {wf.tasks.map((task, idx) => (
                    <div key={idx} className="text-xs text-slate-300 flex items-start gap-1">
                      <CornerDownRight size={12} className="text-indigo-400 mt-0.5 flex-shrink-0" />
                      <div>
                        <span className="font-mono text-indigo-300">{task.taskTemplateId}:</span>{' '}
                        <span className="font-semibold">{task.name}</span>
                        {task.dependencies && (
                          <span className="text-[10px] font-mono text-slate-500 ml-1.5">
                            (depends on: {task.dependencies})
                          </span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>

          <div className="flex items-center gap-2 justify-end self-end md:self-center">
            <button 
              disabled={triggering === wf.id}
              onClick={() => handleRun(wf.id)}
              className="px-4 py-2 bg-indigo-500/10 hover:bg-indigo-500 text-indigo-400 hover:text-white disabled:opacity-50 rounded text-xs font-semibold flex items-center gap-1.5 transition-all cursor-pointer border border-indigo-500/20"
            >
              {triggering === wf.id ? (
                <>
                  <Loader2 size={12} className="animate-spin" /> Triggering...
                </>
              ) : (
                <>
                  <Play size={12} /> Run Trigger
                </>
              )}
            </button>
          </div>
        </div>
      ))}
    </div>
  );
};

// ==========================================
// 4. DAG & SPOTLIGHT VISUALIZERS
// ==========================================

export const DAGView: React.FC<{
  graph: {
    nodes: Array<{
      id: string;
      type: string;
      action: string;
      instructions: string;
      status: string;
      output?: string;
      staticArgs?: string;
    }>;
    edges: Array<{
      sourceId: string;
      targetId: string;
    }>;
  };
  nodeStates?: Record<string, {
    status: string;
    output?: string;
    rawOutput?: string;
  }>;
}> = ({ graph, nodeStates = {} }) => {
  const [selectedNode, setSelectedNode] = useState<any | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [coords, setCoords] = useState<Record<string, { x: number; y: number }>>({});
  const [, setResizeCount] = useState(0);

  // Group nodes by topological level dynamically
  const getLevels = () => {
    const inDegree: Record<string, number> = {};
    const adjList: Record<string, string[]> = {};
    const nodeMap: Record<string, any> = {};

    graph.nodes.forEach((n) => {
      inDegree[n.id] = 0;
      adjList[n.id] = [];
      nodeMap[n.id] = n;
    });

    graph.edges.forEach((e) => {
      if (adjList[e.sourceId] && inDegree[e.targetId] !== undefined) {
        adjList[e.sourceId].push(e.targetId);
        inDegree[e.targetId]++;
      }
    });

    const queue: string[] = [];
    Object.keys(inDegree).forEach((id) => {
      if (inDegree[id] === 0) queue.push(id);
    });

    const levels: string[][] = [];
    while (queue.length > 0) {
      const levelSize = queue.length;
      const currentLevel: string[] = [];
      for (let i = 0; i < levelSize; i++) {
        const id = queue.shift()!;
        currentLevel.push(id);
        adjList[id].forEach((child) => {
          inDegree[child]--;
          if (inDegree[child] === 0) queue.push(child);
        });
      }
      levels.push(currentLevel);
    }

    return { levels, nodeMap };
  };

  const { levels, nodeMap } = getLevels();

  const updateCoordinates = () => {
    if (!containerRef.current) return;
    const containerRect = containerRef.current.getBoundingClientRect();
    const newCoords: Record<string, { x: number; y: number }> = {};

    graph.nodes.forEach((n) => {
      const el = document.getElementById(`dag-node-${n.id}`);
      if (el) {
        const elRect = el.getBoundingClientRect();
        newCoords[n.id] = {
          x: elRect.left - containerRect.left + elRect.width / 2,
          y: elRect.top - containerRect.top + elRect.height / 2,
        };
      }
    });

    setCoords(newCoords);
  };

  useLayoutEffect(() => {
    updateCoordinates();
  }, [graph, levels.length]);

  useEffect(() => {
    const handleResize = () => {
      setResizeCount(prev => prev + 1);
      updateCoordinates();
    };
    window.addEventListener('resize', handleResize);
    // Extra timeout triggers to handle layouts settling
    const t1 = setTimeout(updateCoordinates, 100);
    const t2 = setTimeout(updateCoordinates, 500);
    return () => {
      window.removeEventListener('resize', handleResize);
      clearTimeout(t1);
      clearTimeout(t2);
    };
  }, [graph]);

  return (
    <div className="relative">
      <div 
        ref={containerRef} 
        className="relative min-h-[350px] bg-slate-950/40 border border-[var(--glass-border)] rounded-xl p-8 flex flex-row justify-between items-center overflow-x-auto gap-8"
      >
        {/* SVG Overlay for drawing dependency lines */}
        <svg className="absolute inset-0 w-full h-full pointer-events-none z-0">
          <defs>
            <marker id="arrow" viewBox="0 0 10 10" refX="22" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--muted-color)" opacity="0.4" />
            </marker>
            <marker id="arrow-active" viewBox="0 0 10 10" refX="22" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--accent-color)" />
            </marker>
          </defs>
          {graph.edges.map((edge, idx) => {
            const start = coords[edge.sourceId];
            const end = coords[edge.targetId];
            if (!start || !end) return null;

            const sourceNode = nodeMap[edge.sourceId];
            const targetNode = nodeMap[edge.targetId];
            const isCompleted = (sourceNode?.status === 'completed' || nodeStates[edge.sourceId]?.status === 'completed') &&
                              (targetNode?.status === 'running' || nodeStates[edge.targetId]?.status === 'running' || targetNode?.status === 'completed' || nodeStates[edge.targetId]?.status === 'completed');

            // Draw clean curved cubic bezier connections
            const dx = end.x - start.x;
            const control1X = start.x + dx * 0.4;
            const control2X = start.x + dx * 0.6;
            const pathData = `M ${start.x} ${start.y} C ${control1X} ${start.y}, ${control2X} ${end.y}, ${end.x} ${end.y}`;

            return (
              <path
                key={idx}
                d={pathData}
                fill="none"
                stroke={isCompleted ? 'var(--accent-color)' : 'var(--glass-border)'}
                strokeWidth={isCompleted ? 2 : 1.5}
                strokeDasharray={isCompleted ? 'none' : '4 4'}
                markerEnd={isCompleted ? 'url(#arrow-active)' : 'url(#arrow)'}
                className="transition-all duration-300"
              />
            );
          })}
        </svg>

        {/* Render Levels as vertical columns */}
        {levels.map((levelNodeIds, levelIdx) => (
          <div key={levelIdx} className="flex flex-col gap-6 items-center z-10 flex-shrink-0">
            <div className="text-[10px] uppercase font-bold tracking-widest text-[var(--muted-color)] mb-1">
              Level {levelIdx + 1}
            </div>
            {levelNodeIds.map((nodeId) => {
              const node = nodeMap[nodeId];
              const state = nodeStates[nodeId] || { status: node.status, output: node.output };
              const isSelected = selectedNode?.id === nodeId;

              // Color classes based on node status
              let borderClass = 'border-slate-800';
              let bgClass = 'bg-slate-900/50';
              if (state.status === 'completed') {
                borderClass = 'border-emerald-500/25';
                bgClass = 'bg-emerald-500/5';
              } else if (state.status === 'running') {
                borderClass = 'border-indigo-500/40';
                bgClass = 'bg-indigo-500/10';
              } else if (state.status === 'failed') {
                borderClass = 'border-rose-500/40';
                bgClass = 'bg-rose-500/10';
              }

              return (
                <div
                  key={nodeId}
                  id={`dag-node-${nodeId}`}
                  onClick={() => setSelectedNode({ ...node, state })}
                  className={`glass-panel p-4 w-48 border hover:border-indigo-500/50 transition-all cursor-pointer select-none text-left relative ${borderClass} ${bgClass} ${
                    isSelected ? 'ring-2 ring-indigo-500/60 scale-[1.03]' : ''
                  }`}
                >
                  <div className="flex justify-between items-start">
                    <span className="text-[10px] font-mono uppercase font-bold text-slate-500">
                      {node.type}
                    </span>
                    <span className={`w-2 h-2 rounded-full ${
                      state.status === 'completed' ? 'bg-emerald-400' :
                      state.status === 'running' ? 'bg-indigo-400 animate-pulse' :
                      state.status === 'failed' ? 'bg-rose-400' : 'bg-slate-600'
                    }`}></span>
                  </div>
                  <h5 className="text-xs font-bold text-white mt-1 font-mono truncate">{node.action || "synthesis"}</h5>
                  <p className="text-[10px] text-[var(--muted-color)] mt-1.5 truncate max-w-full">
                    {node.instructions}
                  </p>
                </div>
              );
            })}
          </div>
        ))}
      </div>

      {/* Node Details Inspection Modal Drawer */}
      {selectedNode && (
        <div className="glass-panel p-5 mt-6 border-indigo-500/20 relative animate-fadeIn">
          <button 
            onClick={() => setSelectedNode(null)}
            className="absolute top-4 right-4 text-xs font-semibold text-[var(--muted-color)] hover:text-white"
          >
            ✕ Close
          </button>
          
          <div className="flex items-center gap-3">
            <h4 className="text-base font-bold text-white font-mono">{selectedNode.action || "synthesis"}</h4>
            <StatusBadge status={selectedNode.state.status} />
          </div>

          <div className="mt-4 space-y-3">
            <div>
              <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Instructions / Prompts</div>
              <p className="text-xs text-slate-200 bg-black/15 p-2 rounded border border-white/2 mt-1 leading-relaxed whitespace-pre-wrap font-sans">
                {selectedNode.instructions}
              </p>
            </div>

            {selectedNode.staticArgs && (
              <div>
                <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Pre-Known Static Arguments</div>
                <pre className="text-xs font-mono bg-black/30 text-indigo-300 p-2.5 rounded border border-white/5 overflow-x-auto mt-1 max-h-40">
                  {selectedNode.staticArgs}
                </pre>
              </div>
            )}

            {selectedNode.state.output && (
              <div>
                <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Outputs / Telemetry Logs</div>
                <pre className="text-xs font-mono bg-black/30 text-slate-300 p-2.5 rounded border border-white/5 overflow-x-auto mt-1 max-h-48">
                  {selectedNode.state.output}
                </pre>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};

export const TaskSpotlight: React.FC<{
  task?: {
    taskId: string;
    graph: any;
    states: Record<string, any>;
    createdAt: number;
  };
}> = ({ task }) => {
  if (!task) {
    return (
      <div className="glass-panel p-8 text-center text-[var(--muted-color)] flex flex-col items-center justify-center h-60">
        <HelpCircle size={32} className="mb-2 text-slate-600" />
        <h4 className="text-sm font-semibold text-white">No Spotlight Selected</h4>
        <p className="text-xs mt-1 max-w-xs">Select a task from the list or trigger a regeneration to visualize execution flows.</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-2 border-b border-white/5 pb-3">
        <div>
          <div className="text-xs font-bold text-[var(--muted-color)] flex items-center gap-1.5">
            <Clock size={12} /> Executed: {new Date(task.createdAt * 1000).toLocaleString()}
          </div>
          <h3 className="text-lg font-bold text-white font-mono mt-1 select-all">{task.taskId}</h3>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-[var(--muted-color)]">Nodes count: {task.graph?.nodes?.length || 0}</span>
        </div>
      </div>

      <DAGView graph={task.graph} nodeStates={task.states} />
    </div>
  );
};

export const WorkflowSpotlight: React.FC<{
  execution?: {
    id: string;
    workflowId: string;
    status: string;
    startedAt: number;
    completedAt?: number;
    tokensConsumed: number;
    toolCallsConsumed: number;
  };
  tasks?: Array<{
    taskTemplateId: string;
    taskExecutionId?: string;
    status: string;
    startedAt: number;
    completedAt?: number;
  }>;
}> = ({ execution, tasks = [] }) => {
  if (!execution) {
    return (
      <div className="glass-panel p-8 text-center text-[var(--muted-color)] flex flex-col items-center justify-center h-60">
        <HelpCircle size={32} className="mb-2 text-slate-600" />
        <h4 className="text-sm font-semibold text-white">No Workflow Selected</h4>
        <p className="text-xs mt-1 max-w-xs">Click a workflow definition execution card to reveal spotlight telemetry.</p>
      </div>
    );
  }

  const duration = execution.completedAt 
    ? `${execution.completedAt - execution.startedAt}s` 
    : 'In progress';

  return (
    <div className="space-y-5">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-white/5 pb-3">
        <div>
          <div className="text-xs font-semibold text-[var(--muted-color)]">Workflow Execution ID</div>
          <h3 className="text-base font-bold text-white font-mono mt-0.5 select-all">{execution.id}</h3>
        </div>
        <StatusBadge status={execution.status} />
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Started At</div>
          <div className="text-xs text-white mt-1">{new Date(execution.startedAt * 1000).toLocaleTimeString()}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Total Duration</div>
          <div className="text-xs text-white mt-1">{duration}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Tokens Contained</div>
          <div className="text-xs text-white mt-1">{execution.tokensConsumed}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Tool Executions</div>
          <div className="text-xs text-white mt-1">{execution.toolCallsConsumed}</div>
        </div>
      </div>

      <div className="space-y-3 mt-4">
        <h4 className="text-sm font-semibold text-white flex items-center gap-1.5"><Layers size={14} /> Execution Run Timeline:</h4>
        
        <div className="space-y-3 pl-4 border-l-2 border-indigo-500/20 relative">
          {tasks.map((t, idx) => {
            const taskDuration = t.completedAt ? `${t.completedAt - t.startedAt}s` : 'running';
            return (
              <div key={idx} className="relative last:mb-0">
                {/* Visual node dot */}
                <div className={`absolute -left-[22px] top-1.5 w-2.5 h-2.5 rounded-full ring-4 ring-[#080c14] ${
                  t.status === 'completed' ? 'bg-emerald-400' :
                  t.status === 'failed' ? 'bg-rose-400' :
                  t.status === 'running' ? 'bg-indigo-400 animate-pulse' : 'bg-slate-600'
                }`}></div>

                <div className="glass-panel p-3.5 max-w-full">
                  <div className="flex justify-between items-start gap-2">
                    <div>
                      <span className="text-xs font-mono font-bold text-indigo-300">{t.taskTemplateId}</span>
                      {t.taskExecutionId && (
                        <span className="text-[9px] font-mono text-[var(--muted-color)] block mt-0.5 select-all">
                          ID: {t.taskExecutionId}
                        </span>
                      )}
                    </div>
                    <span className="text-[10px] font-semibold text-[var(--muted-color)] uppercase bg-white/3 px-1.5 py-0.5 rounded">
                      {taskDuration}
                    </span>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
};
