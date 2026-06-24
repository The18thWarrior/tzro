import React, { useEffect, useRef } from 'react';

export const EventFeed: React.FC<{
  events: Array<{
    id?: string;
    timestamp: string;
    eventType: string;
    taskId?: string;
    nodeId?: string;
    payload: string;
  }>;
}> = ({ events = [] }) => {
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [events]);

  if (events.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6 font-mono">Connecting to SSE Telemetry stream...</div>;
  }

  return (
    <div ref={scrollRef} className="h-60 overflow-y-auto font-mono text-xs leading-relaxed space-y-2.5 pr-2 bg-[var(--nested-bg)] p-4 rounded-lg border border-[var(--glass-border)]">
      {events.map((ev, idx) => {
        const timeStr = new Date(ev.timestamp).toLocaleTimeString();
        let payloadParsed = ev.payload;
        try {
          // If JSON object, pretty print it
          const parsed = JSON.parse(ev.payload);
          payloadParsed = JSON.stringify(parsed);
        } catch { /* non-JSON payload, use raw string */ }

        let color = "text-[var(--muted-color)]";
        if (ev.eventType.includes("fail") || ev.eventType.includes("error")) {
          color = "text-rose-400";
        } else if (ev.eventType.includes("complete") || ev.eventType.includes("success")) {
          color = "text-emerald-400";
        } else if (ev.eventType.includes("start") || ev.eventType.includes("running")) {
          color = "text-indigo-400";
        } else if (ev.eventType.includes("pause") || ev.eventType.includes("waiting")) {
          color = "text-amber-400";
        }

        return (
          <div key={idx} className="flex items-start gap-2 border-b border-[var(--glass-border)] pb-1.5 last:border-0 last:pb-0">
            <span className="text-[var(--muted-color)] flex-shrink-0 opacity-70">{timeStr}</span>
            <span className={`font-semibold uppercase flex-shrink-0 ${color}`}>[{ev.eventType}]</span>
            {ev.nodeId && <span className="text-indigo-400 font-semibold flex-shrink-0">{ev.nodeId}:</span>}
            <span className="text-[var(--fg-color)] break-all">{payloadParsed}</span>
          </div>
        );
      })}
    </div>
  );
};
