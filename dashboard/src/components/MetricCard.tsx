import React from 'react';

export const MetricCard: React.FC<{
  label: string;
  value: string;
  trend?: 'up' | 'down' | 'stable';
  trendValue?: string;
}> = ({ label, value, trend, trendValue }) => {
  return (
    <div className="glass-panel glass-panel-hover p-5 flex flex-col justify-between h-32 relative overflow-hidden">
      <div className="text-sm font-medium text-[var(--muted-color)]">{label}</div>
      <div className="text-3xl font-bold text-[var(--heading-color)] tracking-tight mt-1">{value}</div>
      {trend && (
        <div className="absolute bottom-5 right-5">
          <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-semibold ${trend === 'up' ? 'bg-emerald-500/10 text-emerald-400' :
              trend === 'down' ? 'bg-rose-500/10 text-rose-400' : 'bg-slate-500/10 text-slate-400'
            }`}>
            {trend === 'up' ? '↑' : trend === 'down' ? '↓' : '•'} {trendValue || trend}
          </span>
        </div>
      )}
    </div>
  );
};
