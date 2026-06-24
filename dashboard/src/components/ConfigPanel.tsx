import React from 'react';
import { Cpu, Shield } from 'lucide-react';

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
          <h4 className="text-sm font-semibold text-[var(--heading-color)]">Model Mode & Active Model</h4>
          <p className="text-xs text-[var(--muted-color)] mt-0.5 capitalize">{config.modelMode} Planning Mode</p>
          <div className="text-xs font-mono bg-[var(--code-bg)] text-[var(--code-fg)] px-2 py-1 rounded mt-2 border border-[var(--glass-border)] break-all">
            {config.activeModel || "No model configured"}
          </div>
        </div>
      </div>
      <div className="glass-panel p-4 flex items-start gap-4">
        <div className="p-3 bg-emerald-500/10 text-emerald-400 rounded-lg"><Shield size={20} /></div>
        <div>
          <h4 className="text-sm font-semibold text-[var(--heading-color)]">Privacy & Engine Settings</h4>
          <p className="text-xs text-[var(--muted-color)] mt-0.5">Level: {config.privacyLevel}</p>
          <div className="flex gap-2 mt-2">
            <span className={`px-2 py-0.5 rounded text-[10px] font-semibold border ${config.sidecarEnabled ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' : 'bg-rose-500/10 text-rose-400 border-rose-500/20'
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
