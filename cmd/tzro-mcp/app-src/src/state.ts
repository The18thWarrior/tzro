// ============================================================
// state.ts — Application state manager
// ============================================================

import type { TaskData } from './api';

export type ViewType = 'overview' | 'detail';

export interface AppStateSnapshot {
  currentView: ViewType;
  selectedNodeId: string | null;
  taskData: TaskData | null;
  taskId: string | null;
  startTime: number | null;
  isTerminal: boolean;
}

type Listener = (state: AppStateSnapshot) => void;

class AppState {
  private _currentView: ViewType = 'overview';
  private _selectedNodeId: string | null = null;
  private _taskData: TaskData | null = null;
  private _taskId: string | null = null;
  private _startTime: number | null = null;
  private _listeners: Set<Listener> = new Set();

  subscribe(fn: Listener): () => void {
    this._listeners.add(fn);
    return () => this._listeners.delete(fn);
  }

  private _notify(): void {
    const snap = this.snapshot();
    this._listeners.forEach((fn) => fn(snap));
  }

  snapshot(): AppStateSnapshot {
    const status = this._taskData?.status || 'pending';
    return {
      currentView: this._currentView,
      selectedNodeId: this._selectedNodeId,
      taskData: this._taskData,
      taskId: this._taskId,
      startTime: this._startTime,
      isTerminal:
        status === 'completed' || status === 'failed' || status === 'cancelled',
    };
  }

  get taskId(): string | null {
    return this._taskId;
  }

  setTaskId(id: string): void {
    this._taskId = id;
    this._notify();
  }

  setTaskData(data: TaskData): void {
    this._taskData = data;
    if (!this._startTime && data.createdAt) {
      this._startTime = data.createdAt * 1000;
    }
    this._notify();
  }

  showOverview(): void {
    this._currentView = 'overview';
    this._selectedNodeId = null;
    this._notify();
  }

  showDetail(nodeId: string): void {
    this._currentView = 'detail';
    this._selectedNodeId = nodeId;
    this._notify();
  }

  get isTerminal(): boolean {
    const s = this._taskData?.status;
    return s === 'completed' || s === 'failed' || s === 'cancelled';
  }
}

// Singleton
export const appState = new AppState();
