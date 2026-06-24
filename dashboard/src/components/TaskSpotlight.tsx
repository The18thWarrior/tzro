import React from 'react';
import { HelpCircle, Clock, Layers } from 'lucide-react';
import { DAGView } from './DAGView';

export const TaskSpotlight: React.FC<{
  task?: {
    taskId: string;
    graph: {
      nodes: Array<{ id: string; type: string; action: string; instructions: string; status: string; output?: string; staticArgs?: string }>;
      edges: Array<{ sourceId: string; targetId: string }>;
    };
    states: Record<string, { status: string; output?: string; rawOutput?: string }>;
    createdAt: number;
  };
}> = ({ task }) => {
  if (!task) {
    return (
      <div className="glass-panel p-8 text-center text-[var(--muted-color)] flex flex-col items-center justify-center h-60">
        <HelpCircle size={32} className="mb-2 text-[var(--muted-color)]" />
        <h4 className="text-sm font-semibold text-[var(--heading-color)]">No Spotlight Selected</h4>
        <p className="text-xs mt-1 max-w-xs">Select a task from the list or trigger a regeneration to visualize execution flows.</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-2 border-b border-[var(--glass-border)] pb-3">
        <div>
          <div className="text-xs font-bold text-[var(--muted-color)] flex items-center gap-1.5">
            <Clock size={12} /> Executed: {new Date(task.createdAt * 1000).toLocaleString()}
          </div>
          <h3 className="text-lg font-bold text-[var(--heading-color)] font-mono mt-1 select-all">{task.taskId}</h3>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-[var(--muted-color)]">Nodes count: {task.graph?.nodes?.length || 0}</span>
        </div>
      </div>

      {task.graph && task.graph.nodes && task.graph.nodes.length > 0 ? (
        <DAGView graph={task.graph} nodeStates={task.states} />
      ) : (
        <div className="glass-panel p-8 text-center text-[var(--muted-color)] flex flex-col items-center justify-center h-40">
          <Layers size={24} className="mb-2 text-[var(--muted-color)]" />
          <p className="text-xs">DAG graph data is not available for this historical task.</p>
        </div>
      )}
    </div>
  );
};
