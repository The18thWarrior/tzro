import React, { useState } from 'react';
import {
  Play, CheckCircle, XCircle, Loader2, Bell, Clock
} from 'lucide-react';

export const NotificationList: React.FC<{
  notifications: Array<{
    id: string;
    source: string;
    type: string;
    title: string;
    message: string;
    taskId?: string;
    workflowId?: string;
    targetId?: string;
    status: string;
    createdAt: number;
  }>;
  onRefresh: () => void;
}> = ({ notifications = [], onRefresh }) => {
  const [actioning, setActioning] = useState<string | null>(null);

  const handleAction = async (id: string, approve: boolean, value?: string) => {
    setActioning(id);
    try {
      const targetNotif = notifications.find(n => n.id === id);
      if (!targetNotif) return;

      if (targetNotif.type === "approval_request") {
        // Actually, let's call the hook approve API or client tool submit API based on the type
        const res = await fetch(`/api/mcp`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            method: 'tzro_hook_approve',
            arguments: {
              taskId: targetNotif.taskId,
              nodeId: targetNotif.targetId,
              approve: approve
            }
          })
        });
        if (res.ok) {
          // Update status in local DB
          await fetch(`/api/notifications/update`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: id, status: 'read' })
          });
        }
      } else if (targetNotif.type === "client_tool_request") {
        const res = await fetch(`/api/mcp`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            method: 'tzro_client_tool_submit',
            arguments: {
              taskId: targetNotif.taskId,
              toolName: targetNotif.title,
              output: value || "Submitting mock action"
            }
          })
        });
        if (res.ok) {
          await fetch(`/api/notifications/update`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: id, status: 'read' })
          });
        }
      }
      onRefresh();
    } catch (err) {
      console.error(err);
    } finally {
      setActioning(null);
    }
  };

  const activeNotifs = notifications.filter(n => n.status === "unread");

  if (activeNotifs.length === 0) {
    return <div className="text-sm text-[var(--muted-color)] text-center py-6">No pending notifications or approval requests.</div>;
  }

  return (
    <div className="space-y-3">
      {activeNotifs.map((n) => {
        const isApproval = n.type === "approval_request";
        const isClientTool = n.type === "client_tool_request";

        return (
          <div key={n.id} className="glass-panel p-4 border border-indigo-500/10 flex flex-col md:flex-row md:items-center justify-between gap-4">
            <div className="flex items-start gap-3">
              <div className={`p-2 rounded-lg mt-0.5 ${isApproval ? 'bg-amber-500/10 text-amber-400' : 'bg-indigo-500/10 text-indigo-400'}`}>
                {isApproval ? <Clock size={16} /> : <Bell size={16} />}
              </div>
              <div>
                <h4 className="text-sm font-semibold text-[var(--heading-color)]">{n.title}</h4>
                <p className="text-xs text-[var(--fg-color)] mt-1">{n.message}</p>
                {n.taskId && <p className="text-[10px] font-mono text-indigo-400 mt-1">Task ID: {n.taskId.substring(0, 10)}...</p>}
              </div>
            </div>

            {(isApproval || isClientTool) && (
              <div className="flex gap-2 justify-end">
                {isApproval ? (
                  <>
                    <button
                      disabled={actioning === n.id}
                      onClick={() => handleAction(n.id, true)}
                      className="px-3 py-1.5 bg-emerald-500 hover:bg-emerald-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                    >
                      {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <CheckCircle size={12} />} Approve
                    </button>
                    <button
                      disabled={actioning === n.id}
                      onClick={() => handleAction(n.id, false)}
                      className="px-3 py-1.5 bg-rose-500 hover:bg-rose-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                    >
                      {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <XCircle size={12} />} Reject
                    </button>
                  </>
                ) : (
                  <button
                    disabled={actioning === n.id}
                    onClick={() => {
                      const val = prompt("Enter results output value for client tool execution:");
                      if (val !== null) handleAction(n.id, true, val);
                    }}
                    className="px-3 py-1.5 bg-indigo-500 hover:bg-indigo-600 disabled:opacity-50 text-white rounded text-xs font-semibold flex items-center gap-1.5 transition-colors"
                  >
                    {actioning === n.id ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />} Submit Result
                  </button>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
};
