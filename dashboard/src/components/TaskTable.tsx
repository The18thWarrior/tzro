import React from 'react';
import { Eye } from 'lucide-react';
import { StatusBadge } from './StatusBadge';

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
  compact?: boolean;
}> = ({ tasks = [], onSelectTask, selectedTaskId, compact = false }) => {
  if (tasks.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No recent tasks recorded in this window.</div>;
  }

  return (
    <div className="w-full">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-[var(--glass-border)] text-xs font-semibold uppercase tracking-wider text-[var(--muted-color)]">
            <th className="py-3 px-4">Task ID</th>
            {!compact && <th className="py-3 px-4">Prompt</th>}
            {!compact && <th className="py-3 px-4">Intent</th>}
            <th className="py-3 px-4">Status</th>
            <th className="py-3 px-4 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {tasks.map((task) => (
            <tr
              key={task.taskId}
              className={`border-b border-[var(--glass-border)] text-sm transition-colors hover:bg-white/2 cursor-pointer ${selectedTaskId === task.taskId ? 'bg-indigo-500/5 border-l-2 border-l-indigo-500' : ''
                }`}
              onClick={() => onSelectTask(task.taskId)}
            >
              <td className="py-3.5 px-4 font-mono text-xs text-indigo-300">{task.taskId.substring(0, 13)}...</td>
              {!compact && <td className="py-3.5 px-4 max-w-[200px] truncate text-[var(--fg-color)]" title={task.prompt}>{task.prompt}</td>}
              {!compact && <td className="py-3.5 px-4 capitalize text-[var(--muted-color)]">{task.intentType || "Task"}</td>}
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
