import React, { useState, useRef, useEffect, useLayoutEffect } from 'react';
import { StatusBadge } from './StatusBadge';

interface DAGNode {
  id: string;
  type: string;
  action: string;
  instructions: string;
  status: string;
  output?: string;
  staticArgs?: string;
  state?: {
    status: string;
    output?: string;
    rawOutput?: string;
  };
}

export const DAGView: React.FC<{
  graph: {
    nodes: DAGNode[];
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
  const [selectedNode, setSelectedNode] = useState<DAGNode | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);
  const [coords, setCoords] = useState<Record<string, { x: number; y: number }>>({});
  const [, setResizeCount] = useState(0);

  // Guard: if graph is missing or has no nodes, show empty state
  const nodes = graph?.nodes || [];
  const edges = graph?.edges || [];

  // Group nodes by topological level dynamically
  const getLevels = () => {
    const inDegree: Record<string, number> = {};
    const adjList: Record<string, string[]> = {};
    const nodeMap: Record<string, DAGNode> = {};

    nodes.forEach((n) => {
      inDegree[n.id] = 0;
      adjList[n.id] = [];
      nodeMap[n.id] = n;
    });

    edges.forEach((e) => {
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

    nodes.forEach((n) => {
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
  }, [nodes.length, edges.length, levels.length]);

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
  }, [nodes.length, edges.length]);

  return (
    <div className="relative">
      <div
        ref={containerRef}
        className="relative min-h-[350px] bg-[var(--nested-bg)] border border-[var(--glass-border)] rounded-xl p-8 flex flex-row justify-between items-center overflow-x-auto gap-8"
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
          {edges.map((edge, idx) => {
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
              let borderClass = 'border-[var(--glass-border)]';
              let bgClass = 'bg-[var(--nested-bg)]';
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
                  className={`glass-panel p-4 w-48 border hover:border-indigo-500/50 transition-all cursor-pointer select-none text-left relative ${borderClass} ${bgClass} ${isSelected ? 'ring-2 ring-indigo-500/60 scale-[1.03]' : ''
                    }`}
                >
                  <div className="flex justify-between items-start">
                    <span className="text-[10px] font-mono uppercase font-bold text-[var(--muted-color)] opacity-80">
                      {node.type}
                    </span>
                    <span className={`w-2 h-2 rounded-full ${state.status === 'completed' ? 'bg-emerald-400' :
                        state.status === 'running' ? 'bg-indigo-400 animate-pulse' :
                          state.status === 'failed' ? 'bg-rose-400' : 'bg-[var(--muted-color)]'
                      }`}></span>
                  </div>
                  <h5 className="text-xs font-bold text-[var(--heading-color)] mt-1 font-mono truncate">{node.action || node.id || "synthesis"}</h5>
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
            className="absolute top-4 right-4 text-xs font-semibold text-[var(--muted-color)] hover:text-[var(--heading-color)]"
          >
            ✕ Close
          </button>

          <div className="flex items-center gap-3">
            <h4 className="text-base font-bold text-[var(--heading-color)] font-mono">{selectedNode.action || selectedNode.id || "synthesis"}</h4>
            <StatusBadge status={selectedNode.state.status} />
          </div>

          <div className="mt-4 space-y-3">
            <div>
              <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Instructions / Prompts</div>
              <p className="text-xs text-[var(--fg-color)] bg-[var(--nested-bg)] p-2 rounded border border-[var(--glass-border)] mt-1 leading-relaxed whitespace-pre-wrap font-sans">
                {selectedNode.instructions}
              </p>
            </div>

            {selectedNode.staticArgs && (
              <div>
                <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Pre-Known Static Arguments</div>
                <pre className="text-xs font-mono bg-[var(--code-bg)] text-[var(--code-fg)] p-2.5 rounded border border-[var(--glass-border)] overflow-x-auto mt-1 max-h-40">
                  {selectedNode.staticArgs}
                </pre>
              </div>
            )}

            {selectedNode.state.output && (
              <div>
                <div className="text-[10px] uppercase font-bold text-[var(--muted-color)]">Outputs / Telemetry Logs</div>
                <pre className="text-xs font-mono bg-[var(--code-bg)] text-[var(--fg-color)] p-2.5 rounded border border-[var(--glass-border)] overflow-x-auto mt-1 max-h-48">
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
