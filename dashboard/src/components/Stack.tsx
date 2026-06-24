import React from 'react';

export const Stack: React.FC<{ children: React.ReactNode; gap?: string }> = ({ children, gap = 'gap-4' }) => {
  return <div className={`flex flex-col ${gap} min-w-0`}>{children}</div>;
};
