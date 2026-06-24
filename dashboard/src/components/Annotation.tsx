import React from 'react';
import { AlertTriangle, Zap } from 'lucide-react';

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
