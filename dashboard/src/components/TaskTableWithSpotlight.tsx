import React from 'react';
import { Eye } from 'lucide-react';
import { TaskTable } from './TaskTable';
import { TaskSpotlight } from './TaskSpotlight';

export const TaskTableWithSpotlight: React.FC<{
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
  selectedTaskDetails?: {
    taskId: string;
    graph: {
      nodes: Array<{ id: string; type: string; action: string; instructions: string; status: string; output?: string; staticArgs?: string }>;
      edges: Array<{ sourceId: string; targetId: string }>;
    };
    states: Record<string, { status: string; output?: string; rawOutput?: string }>;
    createdAt: number;
  };
  onCloseSpotlight?: () => void;
}> = ({ tasks = [], onSelectTask, selectedTaskId, selectedTaskDetails, onCloseSpotlight }) => {
  const hasSpotlight = !!selectedTaskId;

  return (
    <div className="flex flex-col lg:flex-row gap-0 min-w-0" id="task-spotlight-section">
      {/* Task Table — shrinks when spotlight is open on desktop */}
      <div className={`min-w-0 transition-all duration-300 ease-in-out ${
        hasSpotlight ? 'lg:w-[50%] lg:flex-shrink-0' : 'w-full'
      }`}>
        <TaskTable tasks={tasks} onSelectTask={onSelectTask} selectedTaskId={selectedTaskId} compact={hasSpotlight} />
      </div>

      {/* Spotlight Side Panel — desktop: slides in from right; mobile: stacks below */}
      {hasSpotlight && (
        <div className="lg:w-[50%] lg:flex-shrink-0 lg:border-l lg:border-[var(--glass-border)] lg:pl-5 mt-5 lg:mt-0 spotlight-slide-in min-w-0 overflow-auto">
          <div className="glass-panel p-5 relative">
            {/* Close button */}
            <button
              onClick={() => onCloseSpotlight?.()}
              className="absolute top-3 right-3 z-10 p-1.5 rounded-lg bg-white/5 hover:bg-white/10 text-[var(--muted-color)] hover:text-white transition-all text-xs font-semibold border border-[var(--glass-border)] cursor-pointer"
              title="Close spotlight"
            >
              ✕
            </button>
            <div className="text-[10px] uppercase font-bold tracking-widest text-indigo-400 mb-3 flex items-center gap-1.5">
              <Eye size={12} /> Task Spotlight
            </div>
            <TaskSpotlight task={selectedTaskDetails} />
          </div>
        </div>
      )}
    </div>
  );
};
