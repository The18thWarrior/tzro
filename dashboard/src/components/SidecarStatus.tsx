import React from 'react';
import { StatusBadge } from './StatusBadge';

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
          <h3 className="text-lg font-bold text-[var(--heading-color)] flex items-center gap-2">Local Sidecar Node</h3>
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
          <div className="w-full bg-[var(--nested-bg)] rounded-full h-1.5 overflow-hidden border border-[var(--glass-border)]">
            <div className="bg-indigo-500 h-full transition-all duration-300" style={{ width: `${sidecar.manifestProgress}%` }}></div>
          </div>
        </div>
      )}

      {sidecar.ggufModelPath && (
        <div className="mt-4">
          <div className="text-xs text-[var(--muted-color)]">Active model file path:</div>
          <div className="text-xs font-mono bg-[var(--code-bg)] text-[var(--code-fg)] px-2.5 py-1.5 rounded border border-[var(--glass-border)] break-all mt-1">
            {sidecar.ggufModelPath}
          </div>
        </div>
      )}
    </div>
  );
};
