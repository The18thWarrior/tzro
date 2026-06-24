import React, { useState } from 'react';
import { Play, Loader2, Layers, Clock, CornerDownRight } from 'lucide-react';
import { StatusBadge } from './StatusBadge';

export const WorkflowMonitor: React.FC<{
  workflows: Array<{
    id: string;
    name: string;
    description: string;
    triggerType: string;
    triggerConfig: string;
    status: string;
    nextRunAt: number;
    tasks?: Array<{ taskId: string; taskTemplateId?: string; name?: string; dependencies?: string; status: string; createdAt: number }>;
  }>;
  executions?: Array<{
    id: string;
    workflowId: string;
    status: string;
    startedAt: number;
    completedAt: number;
    tokensConsumed: number;
    toolCallsConsumed: number;
  }>;
  onTrigger: (wfId: string) => void;
}> = ({ workflows = [], executions = [], onTrigger }) => {
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

  // Group executions by workflow ID, keep last 3 per workflow
  const execsByWorkflow: Record<string, typeof executions> = {};
  for (const ex of executions) {
    if (!execsByWorkflow[ex.workflowId]) {
      execsByWorkflow[ex.workflowId] = [];
    }
    execsByWorkflow[ex.workflowId].push(ex);
  }
  // Each group is already sorted desc from the API; just slice
  for (const wfId of Object.keys(execsByWorkflow)) {
    execsByWorkflow[wfId] = execsByWorkflow[wfId].slice(0, 3);
  }

  if (workflows.length === 0 && executions.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No workflows currently registered.</div>;
  }

  // If no workflow definitions but we have executions, show execution-only view
  const allWorkflowIds = new Set([
    ...workflows.map(w => w.id),
    ...Object.keys(execsByWorkflow),
  ]);

  return (
    <div className="space-y-4">
      {Array.from(allWorkflowIds).map((wfId) => {
        const wf = workflows.find(w => w.id === wfId);
        const wfExecs = execsByWorkflow[wfId] || [];

        return (
          <div key={wfId} className="glass-panel p-4 flex flex-col gap-4 relative overflow-hidden">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
              <div className="flex-1">
                <div className="flex items-center gap-3">
                  <h4 className="text-base font-bold text-[var(--heading-color)]">{wf?.name || wfId}</h4>
                  {wf && (
                    <span className={`px-2 py-0.5 rounded text-[10px] font-semibold border ${wf.status === 'active' ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' : 'bg-slate-500/10 text-slate-400 border-slate-500/20'
                      }`}>
                      {wf.status}
                    </span>
                  )}
                </div>
                {wf && <p className="text-xs text-[var(--muted-color)] mt-1">{wf.description}</p>}

                {wf && (
                  <div className="flex flex-wrap gap-x-6 gap-y-2 mt-3 text-xs text-[var(--muted-color)]">
                    <div>
                      <span className="font-semibold text-[var(--fg-color)]">Trigger:</span> {wf.triggerType} {wf.triggerConfig ? `(${wf.triggerConfig})` : ''}
                    </div>
                    {wf.nextRunAt > 0 && (
                      <div>
                        <span className="font-semibold text-[var(--fg-color)]">Next Run:</span> {new Date(wf.nextRunAt * 1000).toLocaleString()}
                      </div>
                    )}
                  </div>
                )}

                {wf?.tasks && wf.tasks.length > 0 && (
                  <div className="mt-3.5 pt-3 border-t border-[var(--glass-border)]">
                    <div className="text-xs font-semibold text-[var(--heading-color)] mb-2 flex items-center gap-1"><Layers size={12} /> Configured Task Steps:</div>
                    <div className="space-y-1.5 pl-3 border-l border-indigo-500/20">
                      {wf.tasks.map((task, idx) => (
                        <div key={idx} className="text-xs text-[var(--fg-color)] flex items-start gap-1">
                          <CornerDownRight size={12} className="text-indigo-400 mt-0.5 flex-shrink-0" />
                          <div>
                            <span className="font-mono text-indigo-400">{task.taskTemplateId}:</span>{' '}
                            <span className="font-semibold">{task.name}</span>
                            {task.dependencies && (
                              <span className="text-[10px] font-mono text-[var(--muted-color)] ml-1.5">
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

              {wf && (
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
              )}
            </div>

            {/* Execution History — last 3 runs */}
            {wfExecs.length > 0 && (
              <div className="border-t border-[var(--glass-border)] pt-3">
                <div className="text-xs font-semibold text-[var(--heading-color)] mb-2 flex items-center gap-1">
                  <Clock size={12} /> Recent Runs ({wfExecs.length})
                </div>
                <div className="space-y-2">
                  {wfExecs.map((ex) => {
                    const duration = ex.completedAt && ex.startedAt
                      ? Math.round(ex.completedAt - ex.startedAt)
                      : null;
                    return (
                      <div key={ex.id} className="flex items-center gap-3 text-xs bg-[var(--nested-bg)] rounded-lg px-3 py-2 border border-[var(--glass-border)]">
                        <StatusBadge status={ex.status} />
                        <span className="text-[var(--muted-color)] font-mono text-[10px] truncate max-w-[140px]" title={ex.id}>
                          {ex.id.substring(0, 20)}...
                        </span>
                        <span className="text-[var(--muted-color)] opacity-80">
                          {new Date(ex.startedAt * 1000).toLocaleString()}
                        </span>
                        {duration !== null && (
                          <span className="text-slate-500">
                            ({duration}s)
                          </span>
                        )}
                        {ex.tokensConsumed > 0 && (
                          <span className="text-indigo-400 text-[10px]">
                            {ex.tokensConsumed} tok
                          </span>
                        )}
                        {ex.toolCallsConsumed > 0 && (
                          <span className="text-violet-400 text-[10px]">
                            {ex.toolCallsConsumed} calls
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};
