import React from 'react';
import { CheckCircle, XCircle, Loader2, Clock } from 'lucide-react';

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
      return <span className={`${baseClass} bg-[var(--nested-bg)] text-[var(--muted-color)] border border-[var(--glass-border)]`}>Skipped</span>;
    default:
      return <span className={`${baseClass} bg-[var(--nested-bg)] text-[var(--muted-color)] border border-[var(--glass-border)]`}>{status}</span>;
  }
};
