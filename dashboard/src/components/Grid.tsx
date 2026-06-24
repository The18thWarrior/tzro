import React from 'react';

export const Grid: React.FC<{ children: React.ReactNode; columns?: number }> = ({ children, columns = 3 }) => {
  // Derive a flex-basis from the target column count so items wrap naturally
  const basisMap: Record<number, string> = {
    1: 'basis-full',
    2: 'basis-full md:basis-[calc(50%-0.5rem)]',
    3: 'basis-full md:basis-[calc(50%-0.5rem)] lg:basis-[calc(33.333%-0.67rem)]',
    4: 'basis-full md:basis-[calc(50%-0.5rem)] lg:basis-[calc(25%-0.75rem)]',
  };
  const childBasis = basisMap[columns] || basisMap[3];

  return (
    <div className="flex flex-wrap gap-4">
      {React.Children.map(children, (child) =>
        child ? <div className={`${childBasis} flex-grow min-w-0`}>{child}</div> : null
      )}
    </div>
  );
};
