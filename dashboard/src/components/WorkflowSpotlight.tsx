import React from 'react';
import { HelpCircle, Layers } from 'lucide-react';
import { StatusBadge } from './StatusBadge';

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
        <HelpCircle size={32} className="mb-2 text-[var(--muted-color)]" />
        <h4 className="text-sm font-semibold text-[var(--heading-color)]">No Workflow Selected</h4>
        <p className="text-xs mt-1 max-w-xs">Click a workflow definition execution card to reveal spotlight telemetry.</p>
      </div>
    );
  }

  const duration = execution.completedAt
    ? `${execution.completedAt - execution.startedAt}s`
    : 'In progress';

  return (
    <div className="space-y-5">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-[var(--glass-border)] pb-3">
        <div>
          <div className="text-xs font-semibold text-[var(--muted-color)]">Workflow Execution ID</div>
          <h3 className="text-base font-bold text-[var(--heading-color)] font-mono mt-0.5 select-all">{execution.id}</h3>
        </div>
        <StatusBadge status={execution.status} />
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Started At</div>
          <div className="text-xs text-[var(--fg-color)] mt-1">{new Date(execution.startedAt * 1000).toLocaleTimeString()}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Total Duration</div>
          <div className="text-xs text-[var(--fg-color)] mt-1">{duration}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Tokens Contained</div>
          <div className="text-xs text-[var(--fg-color)] mt-1">{execution.tokensConsumed}</div>
        </div>
        <div className="glass-panel p-3.5">
          <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Tool Executions</div>
          <div className="text-xs text-[var(--fg-color)] mt-1">{execution.toolCallsConsumed}</div>
        </div>
      </div>

      <div className="space-y-3 mt-4">
        <h4 className="text-sm font-semibold text-[var(--heading-color)] flex items-center gap-1.5"><Layers size={14} /> Execution Run Timeline:</h4>

        <div className="space-y-3 pl-4 border-l-2 border-indigo-500/20 relative">
          {tasks.map((t, idx) => {
            const taskDuration = t.completedAt ? `${t.completedAt - t.startedAt}s` : 'running';
            return (
              <div key={idx} className="relative last:mb-0">
                {/* Visual node dot */}
                <div className={`absolute -left-[22px] top-1.5 w-2.5 h-2.5 rounded-full ring-4 ring-[var(--bg-color)] ${t.status === 'completed' ? 'bg-emerald-400' :
                    t.status === 'failed' ? 'bg-rose-400' :
                      t.status === 'running' ? 'bg-indigo-400 animate-pulse' : 'bg-[var(--muted-color)]'
                  }`}></div>

                <div className="glass-panel p-3.5 max-w-full">
                  <div className="flex justify-between items-start gap-2">
                    <div>
                      <span className="text-xs font-mono font-bold text-indigo-400">{t.taskTemplateId}</span>
                      {t.taskExecutionId && (
                        <span className="text-[9px] font-mono text-[var(--muted-color)] block mt-0.5 select-all">
                          ID: {t.taskExecutionId}
                        </span>
                      )}
                    </div>
                    <span className="text-[10px] font-semibold text-[var(--muted-color)] uppercase bg-[var(--nested-bg)] px-1.5 py-0.5 rounded">
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
