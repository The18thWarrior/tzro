import React from 'react';

export const Section: React.FC<{ title: string; children: React.ReactNode; subtitle?: string }> = ({ title, children, subtitle }) => {
  return (
    <div className="glass-panel p-6 min-w-0 overflow-hidden">
      <div className="border-b border-[var(--glass-border)] pb-4 mb-4">
        <h2 className="text-xl font-bold tracking-tight text-[var(--heading-color)] flex items-center gap-2">{title}</h2>
        {subtitle && <p className="text-xs text-[var(--muted-color)] mt-1">{subtitle}</p>}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  );
};
