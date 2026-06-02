import React, { useState, useEffect, useRef } from 'react';
import { 
  Terminal, 
  Database, 
  Workflow, 
  Cpu, 
  Settings, 
  X, 
  Send, 
  AlertTriangle, 
  Link2, 
  Plus, 
  CheckCircle2, 
  Loader2, 
  Layers, 
  BookOpen, 
  RefreshCw, 
  Flame,
  Download,
  Sparkles,
  Globe,
  Tag,
  Trash2,
  Bell,
  Check,
  Maximize2,
  Minimize2
} from 'lucide-react';

// Type definitions matching tzro backend
interface DBNotification {
  id: string;
  status: 'unread' | 'read' | 'dismissed';
  taskId?: string;
  workflowId?: string;
  targetId?: string;
  type?: 'warning' | 'error' | 'action_required' | 'info';
  source: string;
  content: string;
  createdAt: number;
  title: string;
  message: string;
}

interface OpenAPIIntegration {
  id: string;
  name: string;
  openapiSpec: string;
  authType: string;
  authKey?: string;
  authValue?: string;
  createdAt: number;
}

interface FactMemory {
  id: string;
  type: string;
  content: string;
  context?: string;
  confidence: number;
}

interface KGNode {
  id: string;
  nodeType: string;
  name: string;
  x?: number;
  y?: number;
}

interface KGEdge {
  id: string;
  edgeType: string;
  sourceId: string;
  targetId: string;
  weight?: number;
}

interface GraphNode {
  id: string;
  type: string;
  action: string;
  instructions: string;
  allowedTools: string[];
  status: string;
  output?: string;
}

interface GraphEdge {
  sourceId: string;
  targetId: string;
}

interface ExecutionGraph {
  taskId: string;
  nodes: GraphNode[];
  edges: GraphEdge[];
  maxCycles: number;
  createdAt: number;
}

interface TaskState {
  taskId: string;
  graph: ExecutionGraph;
  states: Record<string, { status: string; output: string }>;
  createdAt: number;
}

interface Skill {
  id: string;
  name: string;
  triggerDescription: string;
  sopContent: string;
}

interface MCPDaemon {
  command: string;
  args: string[];
  env?: Record<string, string>;
}

interface EntityType {
  id: string;
  label: string;
  color: string;
  icon: string;
  builtIn: boolean;
}

interface EngineConfig {
  modelMode: string;
  cloudProvider: string;
  cloudApiKey: string;
  ggufModelPath: string;
  speedFloor: number;
  sidecarEnabled: boolean;
  modelsDir: string;
}

interface ModelCatalogEntry {
  id: string;
  displayName: string;
  params: string;
  sizeBytes: number;
  sizeLabel: string;
  downloadUrl: string;
  filename: string;
  description: string;
  toolCallTier: string;
  isDefault: boolean;
  downloaded: boolean;
}

interface SidecarStatus {
  activePort: number;
  activePid: number;
  status: string;
  manifestProgress: number;
  ggufModelPath: string;
}

interface ChatMessage {
  sender: string;
  text: string;
  type: 'system' | 'user' | 'agent' | 'promotion' | 'audit';
  time: string;
  streamId?: string;
  isStreaming?: boolean;
}

interface StreamChunk {
  streamId?: string;
  source: string;
  taskId?: string;
  nodeId?: string;
  type: string;
  content: string;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
  };
}

export default function App() {
  // Real-time state
  const [chatInput, setChatInput] = useState('');
  const [notifications, setNotifications] = useState<DBNotification[]>([]);
  const [isNotifDrawerOpen, setIsNotifDrawerOpen] = useState(false);
  const [isChatFullscreen, setIsChatFullscreen] = useState(false);
  const [chatHistory, setChatHistory] = useState<ChatMessage[]>([
    { sender: 'System', text: 'Ready. Ask me to sync databases or trigger workflows!', type: 'system', time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }
  ]);
  const [activeTaskId, setActiveTaskId] = useState<string | null>(null);
  const [taskStatus, setTaskStatus] = useState<string>('No active task');
  const [isTaskCompleted, setIsTaskCompleted] = useState<boolean>(false);

  // Refs to prevent stale closure bugs in SSE event subscription handlers
  const activeTaskIdRef = useRef<string | null>(null);
  const currentGraphRef = useRef<ExecutionGraph | null>(null);
  const currentNodeStatesRef = useRef<Record<string, { status: string; output: string }>>({});

  const setActiveTaskIdWithRef = (id: string | null) => {
    activeTaskIdRef.current = id;
    setActiveTaskId(id);
  };

  const setCurrentGraphWithRef = (graph: ExecutionGraph | null) => {
    currentGraphRef.current = graph;
    setCurrentGraph(graph);
  };

  const setCurrentNodeStatesWithRef = (states: Record<string, { status: string; output: string }>) => {
    currentNodeStatesRef.current = states;
    setCurrentNodeStates(states);
  };
  
  // Data list states
  const [facts, setFacts] = useState<FactMemory[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [mcpDaemons, setMcpDaemons] = useState<Record<string, MCPDaemon>>({});
  const [entityTypes, setEntityTypes] = useState<EntityType[]>([]);
  
  // Graph-RAG neighborhood states
  const [neighborhoodCenter, setNeighborhoodCenter] = useState('con_alice');
  const [canvasNodes, setCanvasNodes] = useState<KGNode[]>([]);
  const [canvasEdges, setKGEdges] = useState<KGEdge[]>([]);
  const [newNodeId, setNewNodeId] = useState('');
  const [newNodeName, setNewNodeName] = useState('');
  const [newNodeType, setNewNodeType] = useState('contact');
  const [showAddType, setShowAddType] = useState(false);
  const [newTypeId, setNewTypeId] = useState('');
  const [newTypeLabel, setNewTypeLabel] = useState('');
  const [newTypeColor, setNewTypeColor] = useState('#6366f1');

  // Config and settings state
  const [config, setConfig] = useState<EngineConfig>({
    modelMode: 'cooperative',
    cloudProvider: 'google',
    cloudApiKey: '',
    ggufModelPath: '',
    speedFloor: 5.0,
    sidecarEnabled: true,
    modelsDir: ''
  });
  
  // Sidecar state
  const [sidecar, setSidecar] = useState<SidecarStatus>({
    activePort: 0,
    activePid: 0,
    status: 'Stopped',
    manifestProgress: 100,
    ggufModelPath: ''
  });

  // Model catalog state
  const [models, setModels] = useState<ModelCatalogEntry[]>([]);
  const [selectedModelId, setSelectedModelId] = useState<string>('');
  const [isDownloading, setIsDownloading] = useState(false);
  const [customModelUrl, setCustomModelUrl] = useState('');

  // UI state
  const [showPreemptionBanner, setShowPreemptionBanner] = useState(false);

  // Active DAG representation states
  const [currentGraph, setCurrentGraph] = useState<ExecutionGraph | null>(null);
  const [currentLevels, setCurrentLevels] = useState<string[][]>([]);
  const [currentNodeStates, setCurrentNodeStates] = useState<Record<string, { status: string; output: string }>>({});

  // Canvas ref
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const draggedNodeRef = useRef<KGNode | null>(null);
  const chatEndRef = useRef<HTMLDivElement | null>(null);

  // Tab Selection
  const [activeTab, setActiveTab] = useState<'tactics' | 'workflows' | 'settings'>('tactics');

  // OpenAPI integrations state hooks
  const [openapiIntegrations, setOpenapiIntegrations] = useState<OpenAPIIntegration[]>([]);
  const [oaId, setOaId] = useState('');
  const [oaName, setOaName] = useState('');
  const [oaAuthType, setOaAuthType] = useState('none');
  const [oaAuthKey, setOaAuthKey] = useState('');
  const [oaAuthValue, setOaAuthValue] = useState('');
  const [oaSpec, setOaSpec] = useState('');
  const [oaError, setOaError] = useState('');
  const [oaSuccess, setOaSuccess] = useState('');

  // Workflow Types
  interface WorkflowTask {
    workflowId: string;
    taskTemplateId: string;
    name: string;
    instructions: string;
    dependencies: string;
  }

  interface WorkflowWithTasks {
    id: string;
    name: string;
    description: string;
    triggerType: string;
    triggerConfig: string;
    status: string;
    nextRunAt: number;
    createdAt: number;
    updatedAt: number;
    tasks: WorkflowTask[];
  }

  interface WorkflowExecution {
    id: string;
    workflowId: string;
    status: string;
    startedAt: number;
    completedAt?: number;
  }

  interface WorkflowTaskExecution {
    workflowExecutionId: string;
    taskTemplateId: string;
    taskExecutionId: string;
    status: string;
    startedAt: number;
    completedAt?: number;
  }

  // Workflows states
  const [workflows, setWorkflows] = useState<WorkflowWithTasks[]>([]);
  const [selectedWorkflow, setSelectedWorkflow] = useState<WorkflowWithTasks | null>(null);
  const [executions, setExecutions] = useState<WorkflowExecution[]>([]);
  const [selectedExecution, setSelectedExecution] = useState<WorkflowExecution | null>(null);
  const selectedExecutionRef = useRef<WorkflowExecution | null>(null);
  const [executionDetails, setExecutionDetails] = useState<WorkflowTaskExecution[]>([]);
  const [selectedWfTask, setSelectedWfTask] = useState<WorkflowTask | null>(null);
  const [workflowLogs, setWorkflowLogs] = useState<string[]>([]);

  // Sliding workflow drawer states
  const [isWfDrawerOpen, setIsWfDrawerOpen] = useState(false);
  const [editingWfId, setEditingWfId] = useState<string | null>(null);
  const [wfName, setWfName] = useState('');
  const [wfDesc, setWfDesc] = useState('');
  const [wfTriggerType, setWfTriggerType] = useState<string>('manual');
  const [wfTriggerConfig, setWfTriggerConfig] = useState('');
  const [wfTasks, setWfTasks] = useState<Omit<WorkflowTask, 'workflowId'>[]>([]);

  // Individual task fields inside the drawer
  const [drawerTaskId, setDrawerTaskId] = useState('');
  const [drawerTaskName, setDrawerTaskName] = useState('');
  const [drawerTaskInstructions, setDrawerTaskInstructions] = useState('');
  const [drawerTaskDeps, setDrawerTaskDeps] = useState<string[]>([]); // Array of taskTemplateIds

  const setSelectedExecutionWithRef = (exec: WorkflowExecution | null) => {
    selectedExecutionRef.current = exec;
    setSelectedExecution(exec);
  };

  // Initial load
  useEffect(() => {
    fetchMemoriesAndMCP();
    fetchConfig();
    fetchModels();
    fetchOpenapiIntegrations();
    fetchNotifications();
    
    // Hydrate existing tasks and workflows on mount
    pollTasks();
    fetchWorkflows();
    fetchExecutions();
    
    // Polling Llama sidecar (1.5s)
    const sidecarPoll = setInterval(pollSidecar, 1500);

    // Establish persistent SSE client event pipe subscription
    const evtSource = new EventSource('/api/events');

    evtSource.onmessage = (e) => {
      try {
        const chunk: StreamChunk = JSON.parse(e.data);
        const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

        switch (chunk.source) {
          case 'chat': {
            if (chunk.type === 'token') {
              setChatHistory(prev => prev.map(msg => {
                if (msg.streamId === chunk.streamId) {
                  return { ...msg, text: msg.text + chunk.content };
                }
                return msg;
              }));
            } else if (chunk.type === 'done') {
              setChatHistory(prev => prev.map(msg => {
                if (msg.streamId === chunk.streamId) {
                  return { ...msg, isStreaming: false };
                }
                return msg;
              }));
            } else if (chunk.type === 'error') {
              setChatHistory(prev => prev.map(msg => {
                if (msg.streamId === chunk.streamId) {
                  return { ...msg, text: msg.text + `\n[Error: ${chunk.content}]`, isStreaming: false };
                }
                return msg;
              }));
            }
            break;
          }
          case 'executor': {
            if (chunk.type === 'node_state') {
              if (chunk.taskId !== activeTaskIdRef.current) break;
              const state = JSON.parse(chunk.content);
              setCurrentNodeStates(prev => {
                const next = { ...prev, [chunk.nodeId!]: state };
                
                // Recalculate dynamic task status matching active states
                let isRunning = false;
                if (currentGraphRef.current) {
                  currentGraphRef.current.nodes.forEach(n => {
                    const s = (n.id === chunk.nodeId ? state : (prev[n.id] || { status: 'pending' })).status;
                    if (s === 'running' || s === 'pending') {
                      isRunning = true;
                    }
                  });
                }
                setTaskStatus(isRunning ? `Running: ${chunk.taskId!.slice(-8)}` : `Completed: ${chunk.taskId!.slice(-8)}`);
                if (!isRunning) {
                  setIsTaskCompleted(true);
                  fetchMemoriesAndMCP(); // Reload skills and facts
                }
                return next;
              });

              if (state.status === 'completed' || state.status === 'failed') {
                pollTasks(); // Sync fully with backend
              }
            } else if (chunk.type === 'token') {
              if (chunk.taskId !== activeTaskIdRef.current) break;
              setCurrentNodeStates(prev => {
                const existing = prev[chunk.nodeId!] || { status: 'running', output: '' };
                return {
                  ...prev,
                  [chunk.nodeId!]: {
                    ...existing,
                    output: existing.output + chunk.content
                  }
                };
              });
            }
            break;
          }
          case 'workflow_orchestrator': {
            if (chunk.type === 'workflow_state') {
              try {
                const state = JSON.parse(chunk.content);
                // Functional updates to avoid stale state in EventSource closure
                setExecutions(prev => {
                  const exists = prev.some(e => e.id === state.executionId);
                  if (exists) {
                    return prev.map(e => e.id === state.executionId ? { ...e, status: state.status } : e);
                  } else {
                    return [{
                      id: state.executionId,
                      workflowId: state.workflowId,
                      status: state.status,
                      startedAt: Math.floor(Date.now() / 1000)
                    }, ...prev];
                  }
                });

                // Update selected execution if it's the one currently viewed
                if (selectedExecutionRef.current && selectedExecutionRef.current.id === state.executionId) {
                  setExecutionDetails(prev => {
                    const exists = prev.some(t => t.taskTemplateId === state.taskTemplateId);
                    if (exists) {
                      return prev.map(t => t.taskTemplateId === state.taskTemplateId ? { ...t, status: state.taskStatus, taskExecutionId: state.taskExecutionId } : t);
                    } else {
                      return [...prev, {
                        workflowExecutionId: state.executionId,
                        taskTemplateId: state.taskTemplateId,
                        taskExecutionId: state.taskExecutionId,
                        status: state.taskStatus,
                        startedAt: Math.floor(Date.now() / 1000)
                      }];
                    }
                  });
                }

                // Append live execution log
                const logTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
                const logMsg = `[${logTime}] Task ${state.taskTemplateId} -> ${state.taskStatus.toUpperCase()} (Execution: ${state.executionId.slice(-8)})`;
                setWorkflowLogs(prev => [logMsg, ...prev]);

                // Auto-refresh workflows list to update next run / details
                fetchWorkflows();
              } catch (err) {
                console.error('Failed to parse workflow state update:', err);
              }
            }
            break;
          }
          case 'system': {
            if (chunk.type === 'promotion') {
              setChatHistory(prev => [
                ...prev,
                {
                  sender: 'System Promotion',
                  text: chunk.content,
                  type: 'promotion',
                  time: timeStr
                }
              ]);
              pollTasks();
            }
            break;
          }
          case 'observer': {
            if (chunk.type === 'observer_audit') {
              setChatHistory(prev => [
                ...prev,
                {
                  sender: 'Observer Agent',
                  text: chunk.content,
                  type: 'audit',
                  time: timeStr
                }
              ]);
            }
            break;
          }
          case 'notification': {
            if (chunk.type === 'notification_created') {
              try {
                const notif = JSON.parse(chunk.content);
                setNotifications(prev => {
                  const exists = prev.some(n => n.id === notif.id);
                  if (exists) {
                    return prev.map(n => n.id === notif.id ? notif : n);
                  } else {
                    return [notif, ...prev];
                  }
                });
              } catch (e) {
                console.error("Failed to parse SSE notification chunk:", e);
              }
            }
            break;
          }
        }
      } catch (err) {
        console.error('Failed parsing SSE message chunk:', err);
      }
    };

    evtSource.onerror = (e) => {
      console.error('SSE persistent pipe connection lost:', e);
    };

    return () => {
      clearInterval(sidecarPoll);
      evtSource.close();
    };
  }, []);

  // Sync active neighborhood whenever center ID shifts
  useEffect(() => {
    fetchNeighborhood(neighborhoodCenter);
  }, [neighborhoodCenter]);

  // Redraw neighborhood on canvas whenever canvasNodes/canvasEdges updates
  useEffect(() => {
    drawNeighborhoodNetwork();
  }, [canvasNodes, canvasEdges, neighborhoodCenter, entityTypes]);

  // Scroll to bottom of chat history when new messages are added
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatHistory]);

  // Workflow API Methods
  const fetchWorkflows = async () => {
    try {
      const resp = await fetch('/api/workflows');
      if (resp.ok) {
        const data = await resp.json();
        setWorkflows(data || []);
      }
    } catch (err) {
      console.error('Failed to fetch workflows:', err);
    }
  };

  const fetchExecutions = async (wfId?: string) => {
    try {
      const url = wfId ? `/api/workflows/executions?workflowId=${wfId}` : '/api/workflows/executions';
      const resp = await fetch(url);
      if (resp.ok) {
        const data = await resp.json();
        setExecutions(data || []);
      }
    } catch (err) {
      console.error('Failed to fetch executions:', err);
    }
  };

  const fetchExecutionDetails = async (execId: string) => {
    try {
      const resp = await fetch(`/api/workflows/executions/detail?executionId=${execId}`);
      if (resp.ok) {
        const data = await resp.json();
        setExecutionDetails(data.tasks || []);
      }
    } catch (err) {
      console.error('Failed to fetch execution details:', err);
    }
  };

  const handleWorkflowTrigger = async (wfId: string) => {
    try {
      const resp = await fetch('/api/workflows/trigger', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: wfId })
      });
      if (resp.ok) {
        const logTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        setWorkflowLogs(prev => [`[${logTime}] Sent trigger command for workflow ${wfId}`, ...prev]);
        
        // Push Operator chat message
        const chatTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setChatHistory(prev => [
          ...prev,
          {
            sender: 'Operator',
            text: `Manual trigger: Executing workflow ${wfId}`,
            type: 'user',
            time: chatTime
          },
          {
            sender: 'tzro Engine',
            text: `Workflow execution spawned in the background. Trace live state updates in the Workflows Control panel.`,
            type: 'agent',
            time: chatTime
          }
        ]);
        
        // Refresh executions lists after brief delay
        setTimeout(() => fetchExecutions(selectedWorkflow?.id), 500);
      }
    } catch (err) {
      console.error('Failed to trigger workflow:', err);
    }
  };

  const handleWorkflowToggle = async (wfId: string, currentStatus: string) => {
    const nextStatus = currentStatus === 'active' ? 'paused' : 'active';
    try {
      const resp = await fetch('/api/workflows/toggle', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: wfId, status: nextStatus })
      });
      if (resp.ok) {
        fetchWorkflows();
      }
    } catch (err) {
      console.error('Failed to toggle workflow status:', err);
    }
  };

  const handleWorkflowDelete = async (wfId: string) => {
    if (!confirm('Are you sure you want to delete this workflow definition? This will erase all execution histories and cannot be undone.')) {
      return;
    }
    try {
      const resp = await fetch(`/api/workflows?id=${wfId}`, {
        method: 'DELETE'
      });
      if (resp.ok) {
        if (selectedWorkflow?.id === wfId) {
          setSelectedWorkflow(null);
          setSelectedExecutionWithRef(null);
          setExecutionDetails([]);
        }
        fetchWorkflows();
        fetchExecutions();
      }
    } catch (err) {
      console.error('Failed to delete workflow:', err);
    }
  };

  const handleWorkflowSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!wfName.trim()) return;

    const payload = {
      id: editingWfId || undefined,
      name: wfName,
      description: wfDesc,
      triggerType: wfTriggerType,
      triggerConfig: wfTriggerType === 'cron' ? wfTriggerConfig : '',
      tasks: wfTasks
    };

    try {
      const resp = await fetch('/api/workflows', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      if (resp.ok) {
        setIsWfDrawerOpen(false);
        setEditingWfId(null);
        setWfName('');
        setWfDesc('');
        setWfTriggerType('manual');
        setWfTriggerConfig('');
        setWfTasks([]);
        
        fetchWorkflows();
      }
    } catch (err) {
      console.error('Failed to save workflow:', err);
    }
  };

  const computeTaskLevels = (tasks: Omit<WorkflowTask, 'workflowId'>[]) => {
    const levels: Record<string, number> = {};
    
    const getLevel = (taskId: string): number => {
      if (levels[taskId] !== undefined) return levels[taskId];
      const task = tasks.find(t => t.taskTemplateId === taskId);
      if (!task || !task.dependencies) {
        levels[taskId] = 0;
        return 0;
      }
      const deps = task.dependencies.split(',').map(d => d.trim()).filter(Boolean);
      if (deps.length === 0) {
        levels[taskId] = 0;
        return 0;
      }
      let maxLvl = 0;
      deps.forEach(depId => {
        maxLvl = Math.max(maxLvl, getLevel(depId));
      });
      levels[taskId] = maxLvl + 1;
      return maxLvl + 1;
    };

    tasks.forEach(t => getLevel(t.taskTemplateId));
    
    const grouped: Record<number, Omit<WorkflowTask, 'workflowId'>[]> = {};
    tasks.forEach(t => {
      const lvl = levels[t.taskTemplateId] || 0;
      if (!grouped[lvl]) grouped[lvl] = [];
      grouped[lvl].push(t);
    });

    return grouped;
  };

  const handleAddWfTask = () => {
    if (!drawerTaskId.trim() || !drawerTaskName.trim() || !drawerTaskInstructions.trim()) {
      alert('Please fill in Task Template ID, Task Name, and Instructions.');
      return;
    }
    
    // Check for unique Task Template ID
    if (wfTasks.some(t => t.taskTemplateId === drawerTaskId.trim())) {
      alert('A task with this Template ID already exists in this workflow.');
      return;
    }

    const newTask: Omit<WorkflowTask, 'workflowId'> = {
      taskTemplateId: drawerTaskId.trim(),
      name: drawerTaskName.trim(),
      instructions: drawerTaskInstructions.trim(),
      dependencies: drawerTaskDeps.join(',')
    };

    setWfTasks(prev => [...prev, newTask]);
    
    // Reset task fields
    setDrawerTaskId('');
    setDrawerTaskName('');
    setDrawerTaskInstructions('');
    setDrawerTaskDeps([]);
  };

  const handleRemoveWfTask = (templateId: string) => {
    setWfTasks(prev => prev.filter(t => t.taskTemplateId !== templateId));
    setWfTasks(prev => prev.map(t => {
      const deps = t.dependencies.split(',').map(d => d.trim()).filter(Boolean);
      if (deps.includes(templateId)) {
        return {
          ...t,
          dependencies: deps.filter(d => d !== templateId).join(',')
        };
      }
      return t;
    }));
  };

  const fetchNotifications = async () => {
    try {
      const resp = await fetch('/api/notifications');
      if (resp.ok) {
        const data = await resp.json();
        setNotifications(data || []);
      }
    } catch (err) {
      console.error('Failed to fetch notifications:', err);
    }
  };

  const handleMarkRead = async (id: string, status: 'read' | 'dismissed') => {
    try {
      const resp = await fetch('/api/notifications/update', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, status })
      });
      if (resp.ok) {
        setNotifications(prev => prev.map(n => n.id === id ? { ...n, status } : n));
      }
    } catch (err) {
      console.error('Failed to update notification status:', err);
    }
  };

  const handleMarkAllRead = async () => {
    const unread = notifications.filter(n => n.status === 'unread');
    for (const n of unread) {
      await handleMarkRead(n.id, 'read');
    }
  };

  const handleNotifClick = (n: DBNotification) => {
    if (n.status === 'unread') {
      handleMarkRead(n.id, 'read');
    }

    if (n.taskId) {
      setActiveTaskIdWithRef(n.taskId);
      setActiveTab('tactics');
      pollTasks();
    } else if (n.workflowId) {
      setActiveTab('workflows');
    } else if (n.targetId) {
      const tid = n.targetId;
      if (tid.startsWith('con_') || tid.startsWith('acc_') || tid.startsWith('tkt_') || tid.startsWith('doc_')) {
        setNeighborhoodCenter(tid);
        setActiveTab('tactics');
      }
    }

    setIsNotifDrawerOpen(false);
  };

  // Fetch tabular memories, skills, MCP configurations, and entity types
  const fetchMemoriesAndMCP = async () => {
    try {
      // Tabular memories facts
      const memResp = await fetch('/api/memories');
      if (memResp.ok) {
        const memData = await memResp.json();
        setFacts(memData.facts || []);
      }

      // Procedural skills SOPs
      const skillsResp = await fetch('/api/skills');
      if (skillsResp.ok) {
        const skillsData = await skillsResp.json();
        setSkills(skillsData || []);
      }

      // Stdio MCP configurations
      const mcpResp = await fetch('/api/mcp');
      if (mcpResp.ok) {
        const mcpData = await mcpResp.json();
        setMcpDaemons(mcpData || {});
      }

      // Entity type registry
      const etResp = await fetch('/api/entity-types');
      if (etResp.ok) {
        const etData = await etResp.json();
        setEntityTypes(etData || []);
      }
    } catch (err) {
      console.error('Failed to load initial workspace data:', err);
    }
  };

  // Fetch and sync strategic config
  const fetchConfig = async () => {
    try {
      const resp = await fetch('/api/config');
      if (resp.ok) {
        const cfg = await resp.json();
        setConfig(cfg);
        // Auto-select the model that matches current config path
        if (cfg.ggufModelPath) {
          const filename = cfg.ggufModelPath.split('/').pop() || '';
          setSelectedModelId(prev => prev || '__from_config__:' + filename);
        }
      }
    } catch (err) {
      console.error('Failed to query configuration:', err);
    }
  };

  // Fetch model catalog
  const fetchModels = async () => {
    try {
      const resp = await fetch('/api/models');
      if (resp.ok) {
        const data: ModelCatalogEntry[] = await resp.json();
        setModels(data || []);
        // Auto-select: find the downloaded model matching config, or the default
        if (data && data.length > 0) {
          setSelectedModelId(prev => {
            // If we had a config-derived selection, match it
            if (prev.startsWith('__from_config__:')) {
              const filename = prev.replace('__from_config__:', '');
              const match = data.find(m => m.filename === filename);
              if (match) return match.id;
            }
            // If already selected validly, keep it
            if (prev && data.find(m => m.id === prev)) return prev;
            // Otherwise pick the first downloaded, or default
            const downloaded = data.find(m => m.downloaded);
            if (downloaded) return downloaded.id;
            const defaultModel = data.find(m => m.isDefault);
            return defaultModel?.id || data[0].id;
          });
        }
      }
    } catch (err) {
      console.error('Failed to fetch model catalog:', err);
    }
  };

  const fetchOpenapiIntegrations = async () => {
    try {
      const res = await fetch('/api/mcp/openapi');
      if (res.ok) {
        const data = await res.json();
        setOpenapiIntegrations(data || []);
      }
    } catch (err) {
      console.error('Failed to fetch OpenAPI integrations', err);
    }
  };

  // Download a model
  const handleDownloadModel = async (modelId?: string, customUrl?: string) => {
    setIsDownloading(true);
    try {
      const body: Record<string, string> = {};
      if (customUrl) {
        body.customUrl = customUrl;
      } else {
        body.modelId = modelId || selectedModelId;
      }

      const resp = await fetch('/api/models/download', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });

      if (!resp.ok) {
        const errText = await resp.text();
        throw new Error(errText);
      }

      const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { 
        sender: 'Model Manager', 
        text: `Downloading model... Progress will show in the settings panel.`, 
        type: 'system', 
        time: timeStr 
      }]);

      // Start polling for completion — fetch fresh status from API each tick
      // to avoid React stale closure issues with state variables
      const pollDownload = setInterval(async () => {
        try {
          // Check sidecar progress directly from the server
          const sidecarResp = await fetch('/api/sidecar');
          if (sidecarResp.ok) {
            const sidecarData = await sidecarResp.json();
            setSidecar(sidecarData);
          }

          // Refresh model catalog to get updated downloaded status
          const modelsResp = await fetch('/api/models');
          if (modelsResp.ok) {
            const modelsData = await modelsResp.json();
            setModels(modelsData || []);

            // Check if our target model is now downloaded
            const targetId = modelId || selectedModelId;
            const targetModel = modelsData?.find((m: ModelCatalogEntry) => m.id === targetId);
            if (targetModel?.downloaded) {
              clearInterval(pollDownload);
              setIsDownloading(false);
              await fetchConfig();
              const doneTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
              setChatHistory(prev => [...prev, { 
                sender: 'Model Manager', 
                text: `Model download complete! Ready to use.`, 
                type: 'system', 
                time: doneTime 
              }]);
            }
          }
        } catch (err) {
          console.error('Download poll error:', err);
        }
      }, 2000);

      // Safety timeout: stop polling after 30 minutes
      setTimeout(() => {
        clearInterval(pollDownload);
        setIsDownloading(false);
      }, 30 * 60 * 1000);

    } catch (err) {
      console.error('Model download failed:', err);
      setIsDownloading(false);
      const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { 
        sender: 'Model Manager', 
        text: `Download failed: ${(err as Error).message}`, 
        type: 'system', 
        time: timeStr 
      }]);
    }
  };

  // Poll active task states
  const pollTasks = async () => {
    try {
      const resp = await fetch('/api/tasks');
      if (!resp.ok) return;
      const tasks: TaskState[] = await resp.json();
      
      if (!tasks || tasks.length === 0) return;

      // Find actively selected task or get latest task
      let activeTask = tasks.find(t => t.taskId === activeTaskIdRef.current);
      if (!activeTask && tasks.length > 0) {
        activeTask = tasks[tasks.length - 1];
        setActiveTaskIdWithRef(activeTask.taskId);
      }

      if (activeTask) {
        const graph = activeTask.graph;
        setCurrentGraphWithRef(graph);
        setCurrentNodeStatesWithRef(activeTask.states);

        // Generate parallel sorted levels
        const levels: string[][] = [];
        graph.nodes.forEach(n => {
          const lvlIdx = getLevelIndexForNode(n.id);
          while (levels.length <= lvlIdx) {
            levels.push([]);
          }
          if (!levels[lvlIdx].includes(n.id)) {
            levels[lvlIdx].push(n.id);
          }
        });
        setCurrentLevels(levels);

        // Calculate if task completed
        let isRunning = false;
        for (const nid in activeTask.states) {
          const s = activeTask.states[nid].status;
          if (s === 'running' || s === 'pending') {
            isRunning = true;
            break;
          }
        }

        setTaskStatus(isRunning ? `Running: ${activeTask.taskId.slice(-8)}` : `Completed: ${activeTask.taskId.slice(-8)}`);
        
        if (!isRunning && !isTaskCompleted) {
          setIsTaskCompleted(true);
          fetchMemoriesAndMCP(); // Reload skills and facts
        }
      }
    } catch (err) {
      console.error('Failed polling task runner state:', err);
    }
  };

  const getLevelIndexForNode = (nodeId: string): number => {
    const lower = nodeId.toLowerCase();
    if (lower.includes('fetch') || lower.includes('analyze') || lower.includes('cron')) return 0;
    if (lower.includes('dedup') || lower.includes('execute')) return 1;
    return 2;
  };

  // Poll Llama model sidecar
  const pollSidecar = async () => {
    try {
      const resp = await fetch('/api/sidecar');
      if (resp.ok) {
        const status: SidecarStatus = await resp.json();
        setSidecar(status);
      }
    } catch (err) {
      console.error('Failed polling sidecar status:', err);
    }
  };

  // Query entity network neighborhood map
  const fetchNeighborhood = async (entityId: string) => {
    try {
      const resp = await fetch(`/api/memories?neighborhood=${entityId}`);
      if (!resp.ok) return;
      const subgraph = await resp.json();
      if (!subgraph || !subgraph.nodes) return;

      const canvas = canvasRef.current;
      const w = canvas ? canvas.clientWidth : 280;
      const h = 240;

      // Layout nodes circularly around the center node
      const nodes: KGNode[] = subgraph.nodes.map((n: KGNode, i: number) => {
        // Reuse coordinates if already present to prevent jumpy layout resets
        const existing = canvasNodes.find(en => en.id === n.id);
        if (existing) return existing;

        if (n.id === entityId) {
          return { ...n, x: w / 2, y: h / 2 };
        }

        const angle = (i * 2.0 * Math.PI) / (subgraph.nodes.length - 1 || 1);
        const radius = 75;
        return {
          ...n,
          x: w / 2 + Math.cos(angle) * radius,
          y: h / 2 + Math.sin(angle) * radius
        };
      });

      setCanvasNodes(nodes);
      setKGEdges(subgraph.edges || []);
    } catch (err) {
      console.error('Failed fetching neighborhood network data:', err);
    }
  };

  // Canvas interaction listeners setup
  const handleCanvasMouseDown = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const rect = canvas.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;

    // Detect click hit on nodes
    const clickedNode = canvasNodes.find(n => {
      if (n.x === undefined || n.y === undefined) return false;
      const dist = Math.hypot(n.x - mx, n.y - my);
      return dist <= 24; // boundary radius check
    });

    if (clickedNode) {
      draggedNodeRef.current = clickedNode;
    }
  };

  const handleCanvasMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas || !draggedNodeRef.current) return;

    const rect = canvas.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;

    // Dynamic state boundary updating
    draggedNodeRef.current.x = mx;
    draggedNodeRef.current.y = my;

    setCanvasNodes([...canvasNodes]);
  };

  const handleCanvasMouseUp = () => {
    draggedNodeRef.current = null;
  };

  const handleCanvasDoubleClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const rect = canvas.getBoundingClientRect();
    const mx = e.clientX - rect.left;
    const my = e.clientY - rect.top;

    const clickedNode = canvasNodes.find(n => {
      if (n.x === undefined || n.y === undefined) return false;
      return Math.hypot(n.x - mx, n.y - my) <= 24;
    });

    if (clickedNode) {
      setNeighborhoodCenter(clickedNode.id);
    }
  };

  // Interactive node canvas renderer
  const drawNeighborhoodNetwork = () => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext('2d');
    if (!ctx) return;

    const w = canvas.clientWidth;
    const h = canvas.clientHeight;
    
    // Set high-DPI resolution rendering
    const dpr = window.devicePixelRatio || 1;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    ctx.scale(dpr, dpr);

    // Clear Canvas
    ctx.clearRect(0, 0, w, h);

    // 1. Draw connecting edge paths
    ctx.lineWidth = 1.5;
    canvasEdges.forEach(edge => {
      const src = canvasNodes.find(n => n.id === edge.sourceId);
      const tgt = canvasNodes.find(n => n.id === edge.targetId);

      if (src && tgt && src.x !== undefined && src.y !== undefined && tgt.x !== undefined && tgt.y !== undefined) {
        // Outer faint ambient line path
        ctx.strokeStyle = 'rgba(255, 255, 255, 0.08)';
        ctx.beginPath();
        ctx.moveTo(src.x, src.y);
        ctx.lineTo(tgt.x, tgt.y);
        ctx.stroke();

        // Glowing center core indicator path
        ctx.strokeStyle = 'rgba(20, 220, 180, 0.25)';
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(src.x, src.y);
        ctx.lineTo(tgt.x, tgt.y);
        ctx.stroke();
        ctx.lineWidth = 1.5;
      }
    });

    // 2. Draw styled nodes
    canvasNodes.forEach(node => {
      if (node.x === undefined || node.y === undefined) return;
      const isCenter = node.id === neighborhoodCenter;

      // Dynamic palette from entity type registry
      const matchedType = entityTypes.find(et => et.id === node.nodeType);
      const color = matchedType?.color || 'hsl(220, 70%, 55%)';

      // Outer glow boundary for current centered node
      if (isCenter) {
        ctx.shadowBlur = 15;
        ctx.shadowColor = color;
      }

      ctx.fillStyle = color;
      ctx.beginPath();
      ctx.arc(node.x, node.y, isCenter ? 12 : 9, 0, 2 * Math.PI);
      ctx.fill();

      // Reset shadows
      ctx.shadowBlur = 0;

      // Outer transparent halo border ring
      ctx.strokeStyle = 'rgba(255, 255, 255, 0.4)';
      ctx.lineWidth = 1.5;
      ctx.beginPath();
      ctx.arc(node.x, node.y, isCenter ? 14 : 11, 0, 2 * Math.PI);
      ctx.stroke();

      // Node Label Text
      ctx.fillStyle = '#f5f2fc';
      ctx.font = isCenter ? 'bold 10px Inter' : '9px Inter';
      ctx.textAlign = 'center';
      ctx.fillText(node.name, node.x, node.y - 18);
      
      // Node ID Type Tag
      ctx.fillStyle = 'rgba(255, 255, 255, 0.45)';
      ctx.font = '7px Fira Code, monospace';
      ctx.fillText(node.id, node.x, node.y + 22);
    });
  };

  // Submit NL console instructions to server
  const handleChatSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const prompt = chatInput.trim();
    if (!prompt) return;

    const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    
    // Push user message immediately
    setChatHistory(prev => [...prev, { sender: 'Operator', text: prompt, type: 'user', time: timeStr }]);
    setChatInput('');

    // Preemption KV Warning Check: If there's an active running T1/T2 task, trigger warning banner
    const isRunning = taskStatus.includes('Running:');
    if (isRunning) {
      setShowPreemptionBanner(true);
      setTimeout(() => setShowPreemptionBanner(false), 6000);
    }

    try {
      const resp = await fetch('/api/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: prompt })
      });

      if (!resp.ok) {
        throw new Error(`HTTP Error: ${resp.statusText}`);
      }

      const data = await resp.json();
      const responseTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      
      if (data.streamId) {
        // Conversational streaming T0 query
        setChatHistory(prev => [
          ...prev, 
          { 
            sender: 'tzro Engine', 
            text: `[${data.intent.type.toUpperCase()} / ${data.complexity}] -> `, 
            type: 'agent', 
            time: responseTime,
            streamId: data.streamId,
            isStreaming: true
          }
        ]);
      } else {
        // Workflow DAG or deterministic action execution
        setChatHistory(prev => [
          ...prev, 
          { 
            sender: 'tzro Engine', 
            text: `[${data.intent.type.toUpperCase()} / ${data.complexity}] -> ${data.message}`, 
            type: 'agent', 
            time: responseTime 
          }
        ]);
      }

      if (data.taskId) {
        setActiveTaskIdWithRef(data.taskId);
        setIsTaskCompleted(false);
        setTaskStatus(`Running: ${data.taskId.slice(-8)}`);
        if (data.graph) {
          setCurrentGraphWithRef(data.graph);
          
          // Pre-populate initial states to prevent jumpy layout resets
          const initialStates: Record<string, { status: string; output: string }> = {};
          data.graph.nodes.forEach((n: GraphNode) => {
            initialStates[n.id] = { status: 'pending', output: '' };
          });
          setCurrentNodeStatesWithRef(initialStates);

          // Generate parallel sorted levels
          const levels: string[][] = [];
          data.graph.nodes.forEach((n: GraphNode) => {
            const lvlIdx = getLevelIndexForNode(n.id);
            while (levels.length <= lvlIdx) {
              levels.push([]);
            }
            if (!levels[lvlIdx].includes(n.id)) {
              levels[lvlIdx].push(n.id);
            }
          });
          setCurrentLevels(levels);
        }
      }
    } catch (err) {
      const errorTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { sender: 'System Error', text: `Failed to route query: ${(err as Error).message}`, type: 'system', time: errorTime }]);
    }
  };

  // Grow relational knowledge graph node
  const handleAddGraphEntity = async (e: React.FormEvent) => {
    e.preventDefault();
    const id = newNodeId.trim();
    const name = newNodeName.trim();
    if (!id || !name) return;

    try {
      const resp = await fetch('/api/memories/node', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, nodeType: newNodeType, name })
      });

      if (resp.ok) {
        // Automatically link added node to the current active neighborhood center node
        await fetch('/api/memories/edge', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            sourceId: neighborhoodCenter,
            targetId: id,
            edgeType: 'references'
          })
        });

        // Reset input fields
        setNewNodeId('');
        setNewNodeName('');
        
        // Sync states & notify UI chat
        fetchNeighborhood(neighborhoodCenter);
        fetchMemoriesAndMCP(); // Refresh tabular memories panel
        
        const notifyTime = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setChatHistory(prev => [
          ...prev, 
          { 
            sender: 'Graph Link', 
            text: `Added relational entity node ${name} linked directly to ${neighborhoodCenter}`, 
            type: 'system', 
            time: notifyTime 
          }
        ]);
      }
    } catch (err) {
      console.error('Failed to add KG node entity:', err);
    }
  };

  // Save Config settings
  const handleSaveConfig = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const resp = await fetch('/api/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...config,
          sidecarEnabled: config.modelMode !== 'cloud'
        })
      });

      if (resp.ok) {
        const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setChatHistory(prev => [...prev, { sender: 'Settings Sync', text: 'Global configuration parameters synced and persisted successfully.', type: 'system', time: timeStr }]);
      }
    } catch (err) {
      console.error('Config sync failure:', err);
    }
  };

  const handleSaveOpenapiIntegration = async (e: React.FormEvent) => {
    e.preventDefault();
    setOaError('');
    setOaSuccess('');

    if (!oaId || !oaName || !oaSpec || !oaAuthType) {
      setOaError('ID, Name, Auth Type, and OpenAPI Spec are required fields.');
      return;
    }

    try {
      JSON.parse(oaSpec);
    } catch (err) {
      setOaError(`Invalid OpenAPI Spec JSON: ${(err as Error).message}`);
      return;
    }

    try {
      const resp = await fetch('/api/mcp/openapi', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          id: oaId,
          name: oaName,
          openapiSpec: oaSpec,
          authType: oaAuthType,
          authKey: oaAuthKey,
          authValue: oaAuthValue
        })
      });

      if (resp.ok) {
        setOaSuccess('OpenAPI integration saved and registered successfully!');
        setOaId('');
        setOaName('');
        setOaSpec('');
        setOaAuthType('none');
        setOaAuthKey('');
        setOaAuthValue('');
        fetchOpenapiIntegrations();
        fetchMemoriesAndMCP();
        
        const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setChatHistory(prev => [...prev, {
          sender: 'System Router',
          text: `Dynamic OpenAPI integration '${oaName}' successfully loaded and registered into the Tool Registry.`,
          type: 'system',
          time: timeStr
        }]);
      } else {
        const txt = await resp.text();
        setOaError(`Registration failed: ${txt}`);
      }
    } catch (err) {
      setOaError(`Failed to save: ${(err as Error).message}`);
    }
  };

  const handleDeleteOpenapiIntegration = async (id: string) => {
    if (!window.confirm(`Are you sure you want to delete OpenAPI integration '${id}'? This will unregister all of its dynamic tools.`)) {
      return;
    }

    try {
      const resp = await fetch(`/api/mcp/openapi?id=${id}`, {
        method: 'DELETE'
      });

      if (resp.ok) {
        fetchOpenapiIntegrations();
        fetchMemoriesAndMCP();
        
        const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setChatHistory(prev => [...prev, {
          sender: 'System Router',
          text: `OpenAPI integration '${id}' deleted. Associated dynamic tools have been unregistered.`,
          type: 'system',
          time: timeStr
        }]);
      } else {
        const txt = await resp.text();
        alert(`Failed to delete: ${txt}`);
      }
    } catch (err) {
      alert(`Deletion failed: ${(err as Error).message}`);
    }
  };

  // Start sidecar process
  const startSidecarProcess = async () => {
    try {
      await fetch('/api/sidecar', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'start' })
      });
      const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { sender: 'Llama Sidecar', text: 'Local tactical sidecar process startup sequence started.', type: 'system', time: timeStr }]);
    } catch (err) {
      console.error(err);
    }
  };

  // Stop sidecar process
  const stopSidecarProcess = async () => {
    try {
      await fetch('/api/sidecar', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'stop' })
      });
      const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { sender: 'Llama Sidecar', text: 'Sent process SIGINT shutdown command.', type: 'system', time: timeStr }]);
    } catch (err) {
      console.error(err);
    }
  };

  // GC cache wipe slots
  const clearContextCache = async () => {
    try {
      await fetch('/api/sidecar', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'erase_cache' })
      });
      const timeStr = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
      setChatHistory(prev => [...prev, { sender: 'GC Cache Wiped', text: 'Cleared persistent SQLite caches and deleted slot logs.', type: 'system', time: timeStr }]);
    } catch (err) {
      console.error(err);
    }
  };

  // Helper mapping sidecar states to status styles
  const getSidecarStatusInfo = () => {
    switch (sidecar.status) {
      case 'Active':
        return { label: 'Active', pillClass: 'bg-emerald-500/20 text-emerald-400 border-emerald-500/30' };
      case 'Starting':
        return { label: 'Starting', pillClass: 'bg-amber-500/20 text-amber-400 border-amber-500/30 animate-pulse' };
      case 'Adopted':
        return { label: 'Adopted', pillClass: 'bg-cyan-500/20 text-cyan-400 border-cyan-500/30' };
      default:
        return { label: 'Offline', pillClass: 'bg-rose-500/10 text-rose-400 border-rose-500/20' };
    }
  };

  const sidecarPill = getSidecarStatusInfo();

  return (
    <div className="relative min-h-screen pb-12 overflow-hidden px-4 md:px-8" data-active-task={activeTaskId || ''}>
      {/* Background glowing blob lights */}
      <div className="bg-blob blob-purple" />
      <div className="bg-blob blob-teal" />
      <div className="bg-blob blob-rose" />

      {/* Floating Priority KV Preemption warning banner */}
      <div 
        className={`fixed top-6 right-6 z-50 glass-panel max-w-sm rounded-xl p-4 border border-rose-500/30 shadow-lg shadow-rose-950/20 transition-all duration-500 transform ${
          showPreemptionBanner ? 'translate-y-0 opacity-100' : '-translate-y-12 opacity-0 pointer-events-none'
        }`}
      >
        <div className="flex items-start gap-3">
          <div className="p-2 rounded-lg bg-rose-500/20 text-rose-400">
            <AlertTriangle className="w-5 h-5 animate-bounce" />
          </div>
          <div>
            <h4 className="font-bold text-sm text-rose-300 flex items-center gap-1.5">
              Priority KV Preemption Alert
            </h4>
            <p className="text-xs text-rose-200/70 mt-1 leading-relaxed">
              Active background workflow context thread preempted. Attention cache slot-0 exported to disk. Priority console prompt served...
            </p>
          </div>
        </div>
      </div>

      {/* Global Split-Pane Container */}
      <div className="max-w-[1600px] mx-auto grid grid-cols-1 lg:grid-cols-12 gap-8 mt-6 lg:h-[calc(100vh-6rem)] lg:overflow-hidden w-full">
        
        {/* Left Column: Dashboard details & main active workspace controls (cols: 8) */}
        <div className={`flex flex-col gap-6 w-full ${isChatFullscreen ? 'hidden' : 'lg:col-span-8 lg:h-full lg:min-h-0 lg:overflow-y-auto pr-2 custom-scrollbar'}`}>
          
          {/* Navigation Header */}
          <header className="w-full rounded-2xl glass-panel p-6 flex flex-col xl:flex-row items-start xl:items-center justify-between gap-6 border border-white/10 shadow-xl">
            
            {/* Left: Brand Identity info */}
            <div className="flex items-center gap-4 shrink-0">
              <div className="w-12 h-12 rounded-2xl bg-gradient-to-tr from-purple-600 to-teal-400 flex items-center justify-center font-bold text-2xl shadow-lg shadow-purple-950/60 border border-white/10">
                t
              </div>
              <div>
                <h1 className="text-2xl font-black tracking-tight text-white m-0 leading-none">tzro</h1>
                <span className="text-xs font-semibold text-white/40 block mt-1.5 uppercase tracking-wider">Durable Agentic Engine</span>
              </div>
            </div>

            {/* Right: Spaced metrics, status gauges & control actions */}
            <div className="flex flex-col sm:flex-row flex-wrap xl:flex-nowrap items-start sm:items-center gap-4 w-full xl:w-auto xl:justify-end">
              
              {/* Status Gauges block */}
              <div className="flex flex-wrap items-center gap-2">
                <div className="status-pill px-3 py-1.5 rounded-xl border border-emerald-500/30 bg-emerald-500/10 text-emerald-400 flex items-center gap-2 text-xs font-bold shadow-sm">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 glow-indicator shrink-0" />
                  Engine: Online
                </div>

                <div className={`status-pill px-3 py-1.5 rounded-xl border flex items-center gap-2 text-xs font-bold shadow-sm ${sidecarPill.pillClass}`}>
                  <span className={`w-1.5 h-1.5 rounded-full bg-current shrink-0 ${sidecar.status === 'Starting' ? 'animate-ping' : ''}`} />
                  Llama-Server: {sidecarPill.label}
                </div>

                <div className="status-pill px-3 py-1.5 rounded-xl border border-teal-500/20 bg-teal-500/5 text-teal-400 flex items-center gap-2 text-xs font-bold shadow-sm">
                  <span className="w-1.5 h-1.5 rounded-full bg-teal-400 shrink-0" />
                  MCP: {Object.keys(mcpDaemons).length} Active
                </div>

                <div className="status-pill px-3 py-1.5 rounded-xl border border-purple-500/20 bg-purple-500/5 text-purple-400 flex items-center gap-2 text-xs font-bold shadow-sm">
                  <span className="w-1.5 h-1.5 rounded-full bg-purple-400 shrink-0" />
                  Observer: Active
                </div>

                <div className="status-pill px-3 py-1.5 rounded-xl border border-cyan-500/20 bg-cyan-500/5 text-cyan-400 flex items-center gap-2 text-xs font-bold shadow-sm">
                  <span className="w-1.5 h-1.5 rounded-full bg-cyan-400 shrink-0" />
                  Graph DB: Linked
                </div>
              </div>

              {/* Action buttons (Separated for visual balance) */}
              <div className="flex items-center gap-2 shrink-0 border-l border-white/10 pl-0 sm:pl-4 mt-2 sm:mt-0">
                <button 
                  onClick={() => setIsNotifDrawerOpen(true)}
                  className="p-2.5 rounded-xl border border-white/10 bg-white/5 hover:bg-white/10 text-white/70 hover:text-white transition-all cursor-pointer relative shadow-md flex items-center justify-center"
                  title="Durable Notifications"
                >
                  <Bell className="w-4 h-4" />
                  {notifications.filter(n => n.status === 'unread').length > 0 && (
                    <span className="absolute -top-1 -right-1 w-5 h-5 bg-rose-500 rounded-full text-[10px] font-black flex items-center justify-center text-white animate-pulse shadow-md border border-slate-950">
                      {notifications.filter(n => n.status === 'unread').length}
                    </span>
                  )}
                </button>

                <button 
                  onClick={() => { setActiveTab('settings'); fetchModels(); fetchOpenapiIntegrations(); }}
                  className={`p-2.5 rounded-xl border transition-all cursor-pointer shadow-md flex items-center justify-center ${
                    activeTab === 'settings'
                      ? 'bg-teal-500/10 border-teal-500/30 text-teal-400 shadow-lg shadow-teal-500/5'
                      : 'bg-white/5 hover:bg-white/10 border border-white/10 text-white/70 hover:text-white'
                  }`}
                  title="Settings & Configurations"
                >
                  <Settings className="w-4 h-4" />
                </button>
              </div>

            </div>
          </header>
     
          {/* Tab Select Controls */}
          <div className="w-full mb-2 flex gap-4">
            <button 
              onClick={() => setActiveTab('tactics')}
              className={`px-5 py-2.5 rounded-xl border font-bold text-xs uppercase tracking-wider transition-all cursor-pointer flex items-center gap-2 ${
                activeTab === 'tactics' 
                  ? 'bg-teal-500/10 border-teal-500/30 text-teal-400 shadow-lg shadow-teal-500/5' 
                  : 'bg-white/3 border-white/5 text-white/50 hover:text-white/80'
              }`}
            >
              <Layers className="w-4 h-4" />
              Task Tactics
            </button>
            <button 
              onClick={() => {
                setActiveTab('workflows');
                fetchWorkflows();
                fetchExecutions(selectedWorkflow?.id);
              }}
              className={`px-5 py-2.5 rounded-xl border font-bold text-xs uppercase tracking-wider transition-all cursor-pointer flex items-center gap-2 ${
                activeTab === 'workflows' 
                  ? 'bg-teal-500/10 border-teal-500/30 text-teal-400 shadow-lg shadow-teal-500/5' 
                  : 'bg-white/3 border-white/5 text-white/50 hover:text-white/80'
              }`}
            >
              <Workflow className="w-4 h-4" />
              Workflows Control
            </button>
            <button 
              onClick={() => {
                setActiveTab('settings');
                fetchModels();
                fetchOpenapiIntegrations();
              }}
              className={`px-5 py-2.5 rounded-xl border font-bold text-xs uppercase tracking-wider transition-all cursor-pointer flex items-center gap-2 ${
                activeTab === 'settings' 
                  ? 'bg-teal-500/10 border-teal-500/30 text-teal-400 shadow-lg shadow-teal-500/5' 
                  : 'bg-white/3 border-white/5 text-white/50 hover:text-white/80'
              }`}
            >
              <Settings className="w-4 h-4" />
              Settings Config
            </button>
          </div>

          {/* Tactics Panel Content (Nested inside Left Pane) */}
          {activeTab === 'tactics' && (
            <div className="flex flex-col gap-6 animate-fade-in w-full">
          
          {/* Kahn levels visualizer panel */}
          <div className="glass-panel rounded-2xl p-5 flex flex-col min-h-[400px]">
            <div className="flex items-center justify-between border-b border-white/5 pb-3 mb-3">
              <div className="flex items-center gap-2">
                <Workflow className="w-5 h-5 text-teal-400" />
                <h2 className="text-base font-bold text-white leading-none">Kahn DAG Pipeline Tracker</h2>
              </div>
              <span className={`px-2 py-1 rounded text-[10px] font-bold flex items-center gap-1.5 border ${
                taskStatus.includes('Completed') 
                  ? 'bg-emerald-500/20 border-emerald-500/30 text-emerald-400' 
                  : taskStatus.includes('Running')
                    ? 'bg-amber-500/20 border-amber-500/30 text-amber-400 animate-pulse'
                    : 'bg-white/5 border-white/10 text-white/50'
              }`}>
                {taskStatus}
              </span>
            </div>

            <div className="flex-1 overflow-x-auto flex flex-col gap-4">
              {!currentGraph || currentLevels.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center gap-3 text-white/30 italic text-center p-8">
                  <Layers className="w-10 h-10 text-white/15" />
                  <p className="text-xs leading-relaxed max-w-[280px]">
                    Submit a workflow in the Console to see compiled topological DAG pipeline stages run parallel nodes here.
                  </p>
                </div>
              ) : (
                <div className="flex flex-col md:flex-row gap-4 h-full items-stretch">
                  {currentLevels.map((levelNodeIDs, lvlIdx) => (
                    <div key={lvlIdx} className="flex-1 flex flex-col gap-3 p-3 rounded-xl bg-white/2 border border-white/5 min-w-[200px]">
                      <div className="text-[10px] font-bold uppercase tracking-wider text-teal-400/70 border-b border-white/5 pb-1">
                        Stage Level {lvlIdx + 1}
                      </div>

                      <div className="flex-1 flex flex-col gap-3 overflow-y-auto">
                        {levelNodeIDs.map(nodeId => {
                          const node = currentGraph.nodes.find(n => n.id === nodeId);
                          if (!node) return null;
                          const nodeState = currentNodeStates[nodeId] || { status: 'pending', output: '' };

                          let nodeColor = 'border-white/10 bg-white/3 text-white/40';
                          if (nodeState.status === 'running') {
                            nodeColor = 'border-amber-500/50 bg-amber-500/10 text-amber-400';
                          } else if (nodeState.status === 'completed') {
                            nodeColor = 'border-emerald-500/50 bg-emerald-500/10 text-emerald-400';
                          } else if (nodeState.status === 'failed') {
                            nodeColor = 'border-rose-500/50 bg-rose-500/10 text-rose-400';
                          }

                          let cleanOutput = nodeState.output || '';
                          const isLocal = cleanOutput.includes('[Local Tactician]');
                          const isCloud = cleanOutput.includes('[Cloud Fallback]');
                          const isCached = cleanOutput.includes('cacheId');

                          if (isLocal) cleanOutput = cleanOutput.replace('[Local Tactician]', '').trim();
                          if (isCloud) cleanOutput = cleanOutput.replace('[Cloud Fallback]', '').trim();

                          return (
                            <div key={nodeId} className={`p-3 rounded-xl border flex flex-col gap-2 transition-all ${nodeColor}`}>
                              <div className="flex items-center justify-between gap-1.5">
                                <span className="font-mono text-[9px] px-1.5 py-0.5 rounded bg-white/5 font-semibold tracking-wider text-white">
                                  {node.action}
                                </span>
                                <div className="flex gap-1 items-center">
                                  {isLocal && <span className="text-[8px] bg-emerald-500/20 text-emerald-300 px-1 py-0.5 rounded border border-emerald-500/20 font-bold uppercase">Local</span>}
                                  {isCloud && <span className="text-[8px] bg-purple-500/20 text-purple-300 px-1 py-0.5 rounded border border-purple-500/20 font-bold uppercase">Cloud</span>}
                                  {isCached && <span className="text-[8px] bg-cyan-500/20 text-cyan-300 px-1 py-0.5 rounded border border-cyan-500/20 font-bold uppercase flex items-center gap-0.5"><Database className="w-2.5 h-2.5"/>Cached</span>}
                                  <span className={`w-1.5 h-1.5 rounded-full bg-current ${nodeState.status === 'running' ? 'animate-pulse' : ''}`} />
                                </div>
                              </div>

                              <div className="text-[10px] font-bold text-white capitalize">
                                {node.id.replace(/_/g, ' ')}
                              </div>

                              <p className="text-[10px] text-white/60 leading-normal">
                                {node.instructions}
                              </p>

                              {cleanOutput && (
                                <pre className="terminal-font text-[8px] bg-slate-950/40 p-2 border border-white/5 rounded-lg text-teal-300 overflow-x-auto max-h-[80px]">
                                  {cleanOutput}
                                </pre>
                              )}
                            </div>
                          );
                        })}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Nested Grid for lower supporting cards */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            
            {/* Sub-column 1: Relational Graph & MCP Hosts */}
            <div className="flex flex-col gap-6">
              
              {/* Canvas Neighborhood view panel */}
              <div className="glass-panel rounded-2xl p-5 flex flex-col">
                <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-2">
                  <Link2 className="w-5 h-5 text-purple-400" />
                  <h2 className="text-base font-bold text-white leading-none">Relational Graph &amp; RAG</h2>
                </div>

                <p className="text-[11px] text-white/50 leading-relaxed mb-3 font-semibold">
                  Traverse nodes &amp; connections up to 2-hops. Double-click a node to center neighborhood.
                </p>

                <div className="flex items-center gap-2 mb-3">
                  <input 
                    type="text" 
                    value={neighborhoodCenter}
                    onChange={(e) => setNeighborhoodCenter(e.target.value)}
                    placeholder="Entity ID (e.g. con_alice)" 
                    className="flex-1 bg-white/5 border border-white/10 rounded-lg px-2.5 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none"
                  />
                  <button 
                    onClick={() => fetchNeighborhood(neighborhoodCenter)}
                    className="p-2 rounded-lg bg-teal-500/10 border border-teal-500/20 text-teal-400 hover:bg-teal-500/20 text-[10px] font-bold uppercase tracking-wider cursor-pointer"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                  </button>
                </div>

                <div className="w-full h-[200px] border border-white/5 bg-slate-950/30 rounded-xl relative overflow-hidden mb-4">
                  <canvas 
                    ref={canvasRef}
                    onMouseDown={handleCanvasMouseDown}
                    onMouseMove={handleCanvasMouseMove}
                    onMouseUp={handleCanvasMouseUp}
                    onDoubleClick={handleCanvasDoubleClick}
                    className="w-full h-full block cursor-crosshair"
                  />
                </div>

                {/* Grow Graph Form */}
                <form onSubmit={handleAddGraphEntity} className="border-t border-white/5 pt-3.5 flex flex-col gap-3">
                  <h3 className="text-xs font-bold text-white uppercase tracking-wider text-teal-400/80 m-0">Grow Knowledge Graph</h3>
                  
                  <div className="grid grid-cols-2 gap-2">
                    <input 
                      type="text" 
                      value={newNodeId}
                      onChange={(e) => setNewNodeId(e.target.value)}
                      placeholder="ID (e.g. con_bob)" 
                      className="bg-white/5 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none"
                      required
                    />
                    <input 
                      type="text" 
                      value={newNodeName}
                      onChange={(e) => setNewNodeName(e.target.value)}
                      placeholder="Name" 
                      className="bg-white/5 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none"
                      required
                    />
                  </div>

                  <div className="flex gap-2">
                    <select 
                      value={newNodeType}
                      onChange={(e) => setNewNodeType(e.target.value)}
                      className="flex-1 bg-slate-900 border border-white/10 rounded-lg px-2 py-1.5 text-xs text-white focus:outline-none"
                    >
                      {entityTypes.map(et => (
                        <option key={et.id} value={et.id}>{et.label}</option>
                      ))}
                    </select>

                    <button 
                      type="submit" 
                      className="px-3 rounded-lg bg-teal-500 text-slate-950 font-bold hover:bg-teal-400 transition-all text-xs flex items-center justify-center gap-1 cursor-pointer shadow-lg shadow-teal-500/10"
                    >
                      <Plus className="w-3.5 h-3.5" />
                      Add
                    </button>
                  </div>
                </form>

                {/* Entity type pills + add custom type */}
                <div className="border-t border-white/5 pt-3 mt-3">
                  <div className="flex items-center justify-between mb-2">
                    <h3 className="text-[10px] font-bold text-white/50 uppercase tracking-wider m-0">Entity Types</h3>
                    <button
                      type="button"
                      onClick={() => setShowAddType(!showAddType)}
                      className="p-1 rounded bg-white/5 hover:bg-white/10 border border-white/10 text-white/50 hover:text-white transition-all cursor-pointer"
                      title="Add custom type"
                    >
                      <Tag className="w-3 h-3" />
                    </button>
                  </div>

                  <div className="flex flex-wrap gap-1.5 mb-2">
                    {entityTypes.map(et => (
                      <div key={et.id} className="flex items-center gap-1 px-2 py-1 rounded-lg border border-white/10 bg-white/3 text-[10px] font-semibold text-white/80">
                        <span className="w-2 h-2 rounded-full shrink-0" style={{ backgroundColor: et.color }} />
                        {et.label}
                        {!et.builtIn && (
                          <button
                            type="button"
                            onClick={async () => {
                              try {
                                const resp = await fetch(`/api/entity-types?id=${et.id}`, { method: 'DELETE' });
                                if (resp.ok) fetchMemoriesAndMCP();
                              } catch (err) { console.error(err); }
                            }}
                            className="ml-0.5 p-0 bg-transparent border-none text-rose-400/60 hover:text-rose-400 cursor-pointer"
                            title={`Remove ${et.label}`}
                          >
                            <Trash2 className="w-2.5 h-2.5" />
                          </button>
                        )}
                      </div>
                    ))}
                  </div>

                  {showAddType && (
                    <div className="flex gap-1.5 items-end">
                      <input
                        type="text"
                        value={newTypeId}
                        onChange={(e) => setNewTypeId(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                        placeholder="id"
                        className="w-16 bg-white/5 border border-white/10 rounded-lg px-1.5 py-1 text-[10px] text-white placeholder-white/30 focus:outline-none"
                      />
                      <input
                        type="text"
                        value={newTypeLabel}
                        onChange={(e) => setNewTypeLabel(e.target.value)}
                        placeholder="Label"
                        className="flex-1 bg-white/5 border border-white/10 rounded-lg px-1.5 py-1 text-[10px] text-white placeholder-white/30 focus:outline-none"
                      />
                      <input
                        type="color"
                        value={newTypeColor}
                        onChange={(e) => setNewTypeColor(e.target.value)}
                        className="w-6 h-6 rounded border border-white/10 bg-transparent cursor-pointer p-0"
                      />
                      <button
                        type="button"
                        onClick={async () => {
                          if (!newTypeId || !newTypeLabel) return;
                          try {
                            const resp = await fetch('/api/entity-types', {
                              method: 'POST',
                              headers: { 'Content-Type': 'application/json' },
                              body: JSON.stringify({ id: newTypeId, label: newTypeLabel, color: newTypeColor })
                            });
                            if (resp.ok) {
                              setNewTypeId('');
                              setNewTypeLabel('');
                              setNewTypeColor('#6366f1');
                              setShowAddType(false);
                              fetchMemoriesAndMCP();
                            }
                          } catch (err) { console.error(err); }
                        }}
                        className="px-2 py-1 rounded-lg bg-teal-500/80 text-slate-950 text-[10px] font-bold hover:bg-teal-400 transition-all cursor-pointer"
                      >
                        Add
                      </button>
                    </div>
                  )}
                </div>
              </div>

              {/* Persistent stdio MCP host servers configurations panel */}
              <div className="glass-panel rounded-2xl p-5 flex flex-col h-[280px]">
                <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-3">
                  <Cpu className="w-5 h-5 text-teal-400" />
                  <h2 className="text-base font-bold text-white leading-none">Stdio MCP Hosts</h2>
                </div>

                <div className="flex-1 overflow-y-auto space-y-3 pr-2">
                  {Object.keys(mcpDaemons).length === 0 ? (
                    <div className="h-full flex items-center justify-center text-xs text-white/30 italic">
                      No daemon configs found
                    </div>
                  ) : (
                    Object.keys(mcpDaemons).map((key) => (
                      <div key={key} className="p-3 rounded-xl bg-white/3 border border-white/5 flex flex-col gap-2">
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-bold text-xs text-white">{key}</span>
                          <span className="px-2 py-0.5 rounded text-[8px] bg-teal-500/20 text-teal-300 border border-teal-500/30 font-bold uppercase">
                            Active Stdio
                          </span>
                        </div>
                        <pre className="terminal-font text-[9px] text-white/40 leading-normal whitespace-pre-wrap break-all">
                          {mcpDaemons[key].command} {mcpDaemons[key].args.join(' ')}
                        </pre>
                      </div>
                    ))
                  )}
                </div>
              </div>

            </div>

            {/* Sub-column 2: Memory Facts & Procedural Skills */}
            <div className="flex flex-col gap-6">
              
              {/* Facts memory log */}
              <div className="glass-panel rounded-2xl p-5 flex flex-col h-[320px]">
                <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-3">
                  <Database className="w-5 h-5 text-purple-400" />
                  <h2 className="text-base font-bold text-white leading-none">Facts Memory &amp; Insights</h2>
                </div>

                <div className="flex-1 overflow-y-auto space-y-3 pr-2">
                  {facts.length === 0 ? (
                    <div className="h-full flex items-center justify-center text-xs text-white/30 italic">
                      No memory elements fetched yet
                    </div>
                  ) : (
                    facts.map((fact) => (
                      <div key={fact.id} className="p-3 rounded-xl bg-white/3 border border-white/5 flex flex-col gap-1.5">
                        <div className="flex items-center justify-between gap-2">
                          <span className="px-2 py-0.5 rounded text-[9px] font-bold uppercase tracking-wider bg-purple-500/20 text-purple-300 border border-purple-500/30">
                            {fact.type.replace('_', ' ')}
                          </span>
                          <span className="text-[10px] text-teal-400 font-semibold">
                            {Math.round(fact.confidence * 100)}% Match
                          </span>
                        </div>
                        <p className="text-xs text-white/90 font-medium leading-relaxed">{fact.content}</p>
                        {fact.context && (
                          <span className="text-[10px] text-white/40 block leading-tight font-mono truncate">{fact.context}</span>
                        )}
                      </div>
                    ))
                  )}
                </div>
              </div>

              {/* Synthesized procedural SOP skills list */}
              <div className="glass-panel rounded-2xl p-5 flex flex-col h-[320px]">
                <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-3">
                  <BookOpen className="w-5 h-5 text-teal-400" />
                  <h2 className="text-base font-bold text-white leading-none">Synthesized SOP Skills</h2>
                </div>

                <div className="flex-1 overflow-y-auto space-y-3 pr-2">
                  {skills.length === 0 ? (
                    <div className="h-full flex items-center justify-center text-xs text-white/30 italic">
                      No procedural skills synthesized yet
                    </div>
                  ) : (
                    skills.map((skill) => (
                      <div key={skill.id} className="p-3 rounded-xl bg-white/3 border border-white/5 flex flex-col gap-2">
                        <div className="font-bold text-xs text-white flex items-center gap-1.5">
                          <CheckCircle2 className="w-3.5 h-3.5 text-teal-400" />
                          {skill.name}
                        </div>
                        <p className="text-[11px] text-white/50 leading-relaxed font-semibold">
                          {skill.triggerDescription}
                        </p>
                        <pre className="terminal-font text-[9px] bg-slate-950/40 p-2.5 rounded-lg border border-white/5 text-purple-300 max-h-[90px] overflow-y-auto">
                          <code>{skill.sopContent}</code>
                        </pre>
                      </div>
                    ))
                  )}
                </div>
              </div>

            </div>

          </div>

        </div>

      )}

      {/* Workflows Control Panel content */}
      {activeTab === 'workflows' && (
        <div className="grid grid-cols-1 xl:grid-cols-12 gap-6 items-start animate-fade-in w-full">
          
          {/* Left Column: Workflows List & Executions History (cols: 4) */}
          <div className="xl:col-span-4 flex flex-col gap-6 w-full">
            
            {/* Workflows List Panel */}
            <div className="glass-panel rounded-2xl p-5 flex flex-col min-h-[300px]">
              <div className="flex items-center justify-between border-b border-white/5 pb-3 mb-4">
                <div className="flex items-center gap-2">
                  <Workflow className="w-5 h-5 text-teal-400" />
                  <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Workflows</h2>
                </div>
                <button
                  onClick={() => {
                    setEditingWfId(null);
                    setWfName('');
                    setWfDesc('');
                    setWfTriggerType('manual');
                    setWfTriggerConfig('');
                    setWfTasks([]);
                    setDrawerTaskId('');
                    setDrawerTaskName('');
                    setDrawerTaskInstructions('');
                    setDrawerTaskDeps([]);
                    setIsWfDrawerOpen(true);
                  }}
                  className="p-1.5 rounded-lg bg-teal-500/10 border border-teal-500/20 text-teal-400 hover:bg-teal-500/20 transition-all cursor-pointer flex items-center justify-center"
                  title="Create New Workflow"
                >
                  <Plus className="w-4 h-4" />
                </button>
              </div>

              <div className="flex-1 overflow-y-auto space-y-3 max-h-[400px] pr-1">
                {workflows.length === 0 ? (
                  <div className="h-full flex items-center justify-center text-xs text-white/30 italic py-8 text-center">
                    No workflows created yet. Click "+" to build one!
                  </div>
                ) : (
                  workflows.map(wf => {
                    const isSelected = selectedWorkflow?.id === wf.id;
                    const isCron = wf.triggerType === 'cron';
                    return (
                      <div 
                        key={wf.id}
                        onClick={() => {
                          setSelectedWorkflow(wf);
                          setSelectedWfTask(null);
                          fetchExecutions(wf.id);
                        }}
                        className={`p-3.5 rounded-xl border transition-all cursor-pointer flex flex-col gap-2 ${
                          isSelected 
                            ? 'border-teal-500/40 bg-teal-500/5 shadow-lg shadow-teal-500/5' 
                            : 'border-white/5 bg-white/2 hover:border-white/10 hover:bg-white/4'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-bold text-xs text-white leading-tight">{wf.name}</span>
                          <span className={`px-2 py-0.5 rounded text-[8px] font-bold uppercase tracking-wider ${
                            wf.status === 'active'
                              ? 'bg-emerald-500/20 text-emerald-400 border border-emerald-500/20'
                              : 'bg-white/5 text-white/40 border border-white/5'
                          }`}>
                            {wf.status}
                          </span>
                        </div>

                        <p className="text-[10px] text-white/50 leading-relaxed font-medium m-0 line-clamp-2">
                          {wf.description || 'No description provided.'}
                        </p>

                        <div className="flex items-center justify-between border-t border-white/5 pt-2 mt-1">
                          <span className="text-[9px] text-teal-400/80 font-semibold uppercase tracking-wider">
                            {isCron ? `Cron: ${wf.triggerConfig}` : 'Manual'}
                          </span>
                          
                          <div className="flex items-center gap-1.5" onClick={e => e.stopPropagation()}>
                            <button
                              onClick={() => handleWorkflowTrigger(wf.id)}
                              className="p-1 rounded bg-white/5 hover:bg-teal-500 hover:text-slate-950 text-white/70 transition-all border border-white/5"
                              title="Trigger manually"
                            >
                              <Send className="w-3 h-3" />
                            </button>
                            <button
                              onClick={() => {
                                setEditingWfId(wf.id);
                                setWfName(wf.name);
                                setWfDesc(wf.description);
                                setWfTriggerType(wf.triggerType);
                                setWfTriggerConfig(wf.triggerConfig);
                                setWfTasks(wf.tasks || []);
                                setDrawerTaskId('');
                                setDrawerTaskName('');
                                setDrawerTaskInstructions('');
                                setDrawerTaskDeps([]);
                                setIsWfDrawerOpen(true);
                              }}
                              className="p-1 rounded bg-white/5 hover:bg-purple-500 text-white/70 transition-all border border-white/5"
                              title="Edit workflow"
                            >
                              <Settings className="w-3 h-3" />
                            </button>
                            <button
                              onClick={() => handleWorkflowToggle(wf.id, wf.status)}
                              className={`p-1 rounded border border-white/5 transition-all ${
                                wf.status === 'active'
                                  ? 'bg-emerald-500/10 text-emerald-400 hover:bg-emerald-500/20'
                                  : 'bg-white/5 text-white/50 hover:bg-white/10'
                              }`}
                              title={wf.status === 'active' ? 'Pause scheduling' : 'Activate scheduling'}
                            >
                              <RefreshCw className="w-3 h-3" />
                            </button>
                            <button
                              onClick={() => handleWorkflowDelete(wf.id)}
                              className="p-1 rounded bg-white/5 hover:bg-rose-500 hover:text-white text-rose-400/80 transition-all border border-white/5"
                              title="Delete workflow"
                            >
                              <Trash2 className="w-3 h-3" />
                            </button>
                          </div>
                        </div>

                        {isCron && wf.status === 'active' && wf.nextRunAt > 0 && (
                          <div className="text-[8px] text-white/30 font-mono mt-0.5">
                            Next run: {new Date(wf.nextRunAt * 1000).toLocaleString()}
                          </div>
                        )}
                      </div>
                    );
                  })
                )}
              </div>
            </div>

            {/* Executions History Panel */}
            <div className="glass-panel rounded-2xl p-5 flex flex-col min-h-[300px]">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-4">
                <Database className="w-5 h-5 text-purple-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Runs History</h2>
              </div>

              <div className="flex-1 overflow-y-auto space-y-2.5 max-h-[350px] pr-1">
                {executions.length === 0 ? (
                  <div className="h-full flex items-center justify-center text-xs text-white/30 italic py-8 text-center">
                    {selectedWorkflow 
                      ? 'No executions found for this workflow.' 
                      : 'Select a workflow on the left to see its history runs.'}
                  </div>
                ) : (
                  executions.map(exec => {
                    const isSelected = selectedExecution?.id === exec.id;
                    let pillColor = 'bg-white/5 border-white/5 text-white/50';
                    if (exec.status === 'running') pillColor = 'bg-amber-500/20 border-amber-500/30 text-amber-400 animate-pulse';
                    else if (exec.status === 'completed') pillColor = 'bg-emerald-500/20 border-emerald-500/30 text-emerald-400';
                    else if (exec.status === 'failed') pillColor = 'bg-rose-500/20 border-rose-500/30 text-rose-400';

                    return (
                      <div
                        key={exec.id}
                        onClick={() => {
                          setSelectedExecutionWithRef(exec);
                          setSelectedWfTask(null);
                          fetchExecutionDetails(exec.id);
                        }}
                        className={`p-3 rounded-xl border transition-all cursor-pointer flex flex-col gap-1.5 ${
                          isSelected 
                            ? 'border-purple-500/40 bg-purple-500/5 shadow-lg shadow-purple-500/5' 
                            : 'border-white/5 bg-white/2 hover:border-white/10 hover:bg-white/4'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-mono text-[10px] font-bold text-white tracking-wider">
                            {exec.id.slice(-12)}
                          </span>
                          <span className={`px-2 py-0.5 rounded text-[8px] font-bold uppercase tracking-wider border ${pillColor}`}>
                            {exec.status}
                          </span>
                        </div>

                        <div className="flex items-center justify-between text-[9px] text-white/40">
                          <span>Started: {new Date(exec.startedAt * 1000).toLocaleTimeString()}</span>
                          {exec.completedAt && exec.completedAt > 0 && (
                            <span>Duration: {exec.completedAt - exec.startedAt}s</span>
                          )}
                        </div>
                      </div>
                    );
                  })
                )}
              </div>
            </div>

          </div>

        {/* Right Column: Execution DAG visualizer & details (cols: 8) */}
        <div className="xl:col-span-8 flex flex-col gap-6 w-full">
            
            <div className="glass-panel rounded-2xl p-5 flex flex-col min-h-[500px]">
              <div className="flex items-center justify-between border-b border-white/5 pb-3 mb-4">
                <div className="flex items-center gap-2">
                  <Workflow className="w-5 h-5 text-teal-400 animate-pulse" />
                  <div>
                    <h2 className="text-base font-bold text-white leading-none">
                      {selectedWorkflow ? `${selectedWorkflow.name} Flow` : 'Execution DAG Flow'}
                    </h2>
                    {selectedExecution && (
                      <span className="text-[10px] font-mono text-purple-400 font-semibold tracking-wider block mt-1">
                        Run ID: {selectedExecution.id}
                      </span>
                    )}
                  </div>
                </div>

                {selectedExecution && (
                  <span className={`px-2.5 py-1 rounded text-[10px] font-bold uppercase tracking-wider border ${
                    selectedExecution.status === 'completed'
                      ? 'bg-emerald-500/20 border-emerald-500/30 text-emerald-400'
                      : selectedExecution.status === 'running'
                        ? 'bg-amber-500/20 border-amber-500/30 text-amber-400 animate-pulse'
                        : 'bg-rose-500/20 border-rose-500/30 text-rose-400'
                  }`}>
                    {selectedExecution.status}
                  </span>
                )}
              </div>

              <div className="flex-1 overflow-x-auto flex flex-col justify-center items-stretch py-4">
                {!selectedWorkflow ? (
                  <div className="flex-1 flex flex-col items-center justify-center gap-3 text-white/30 italic text-center p-8">
                    <Layers className="w-12 h-12 text-white/10" />
                    <p className="text-xs leading-relaxed max-w-[280px]">
                      Select a workflow definition on the left to inspect its details and view live run graphs.
                    </p>
                  </div>
                ) : !selectedExecution ? (
                  <div className="flex-1 flex flex-col items-center justify-center gap-3 text-white/30 italic text-center p-8">
                    <Database className="w-12 h-12 text-white/10" />
                    <p className="text-xs leading-relaxed max-w-[280px]">
                      Select an active or past execution run from history (or click trigger) to load the live-updating pipeline visualizer.
                    </p>
                  </div>
                ) : (
                  (() => {
                    const levelGroup = computeTaskLevels(selectedWorkflow.tasks);
                    const levels = Object.keys(levelGroup).map(Number).sort((a, b) => a - b);

                    return (
                      <div className="flex flex-col md:flex-row gap-6 items-stretch justify-center h-full min-h-[380px]">
                        {levels.map((lvl) => {
                          const lvlTasks = levelGroup[lvl] || [];
                          return (
                            <div key={lvl} className="flex-1 flex flex-col gap-4 p-4 rounded-xl bg-white/2 border border-white/5 min-w-[200px]">
                              <div className="text-[10px] font-bold uppercase tracking-wider text-teal-400/80 border-b border-white/5 pb-2">
                                Stage Level {lvl + 1}
                              </div>

                              <div className="flex-1 flex flex-col gap-4 justify-start">
                                {lvlTasks.map(t => {
                                  // Find execution status
                                  const run = executionDetails.find(ed => ed.taskTemplateId === t.taskTemplateId);
                                  const status = run ? run.status : 'pending';
                                  
                                  let nodeColor = 'border-white/10 bg-white/2 text-white/40';
                                  let statusLabel = 'Pending';
                                  let glowClass = '';

                                  if (status === 'running') {
                                    nodeColor = 'border-amber-500/50 bg-amber-500/10 text-amber-400';
                                    statusLabel = 'Running';
                                    glowClass = 'animate-pulse';
                                  } else if (status === 'completed') {
                                    nodeColor = 'border-emerald-500/50 bg-emerald-500/10 text-emerald-400';
                                    statusLabel = 'Completed';
                                  } else if (status === 'failed') {
                                    nodeColor = 'border-rose-500/50 bg-rose-500/10 text-rose-400';
                                    statusLabel = 'Failed';
                                  }

                                  const isSelected = selectedWfTask?.taskTemplateId === t.taskTemplateId;
                                  const borderHighlight = isSelected ? 'ring-2 ring-teal-400 border-transparent shadow-lg shadow-teal-500/10' : '';

                                  return (
                                    <div 
                                      key={t.taskTemplateId} 
                                      onClick={() => setSelectedWfTask(t as WorkflowTask)}
                                      className={`p-3.5 rounded-xl border flex flex-col gap-2.5 transition-all cursor-pointer hover:scale-[1.02] ${nodeColor} ${borderHighlight}`}
                                    >
                                      <div className="flex items-center justify-between gap-1.5">
                                        <span className="font-mono text-[9px] px-1.5 py-0.5 rounded bg-white/5 font-bold tracking-wider text-white">
                                          {t.taskTemplateId}
                                        </span>
                                        <div className="flex items-center gap-1.5">
                                          <span className="text-[9px] font-medium uppercase tracking-wider">{statusLabel}</span>
                                          <span className={`w-2 h-2 rounded-full bg-current ${glowClass}`} />
                                        </div>
                                      </div>

                                      <div className="text-[11px] font-bold text-white capitalize leading-tight">
                                        {t.name}
                                      </div>

                                      <p className="text-[10px] text-white/60 leading-normal m-0 line-clamp-2">
                                        {t.instructions}
                                      </p>

                                      {t.dependencies && (
                                        <div className="flex flex-wrap gap-1 mt-1">
                                          {t.dependencies.split(',').map(d => (
                                            <span key={d} className="text-[8px] font-mono bg-white/5 border border-white/5 text-white/40 px-1 py-0.2 rounded font-semibold">
                                              dep: {d}
                                            </span>
                                          ))}
                                        </div>
                                      )}
                                    </div>
                                  );
                                })}
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    );
                  })()
                )}
              </div>
            </div>

            {/* Inspector & logs side-by-side underneath */}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-6 w-full">
            
            {/* Task Card Details / Node Output */}
            <div className="glass-panel rounded-2xl p-5 flex flex-col min-h-[280px]">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-3">
                <BookOpen className="w-5 h-5 text-teal-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Inspector</h2>
              </div>

              {selectedWfTask ? (
                (() => {
                  const run = executionDetails.find(ed => ed.taskTemplateId === selectedWfTask.taskTemplateId);
                  
                  return (
                    <div className="flex-1 flex flex-col gap-4 overflow-y-auto max-h-[400px]">
                      <div>
                        <span className="text-[9px] font-mono bg-white/5 text-teal-300 px-1.5 py-0.5 rounded font-bold uppercase tracking-wider">
                          {selectedWfTask.taskTemplateId}
                        </span>
                        <h3 className="text-xs font-bold text-white mt-2 mb-1">{selectedWfTask.name}</h3>
                        <p className="text-[10px] text-white/50 leading-relaxed font-semibold italic">"{selectedWfTask.instructions}"</p>
                      </div>

                      <div className="border-t border-white/5 pt-3">
                        <h4 className="text-[10px] font-bold text-teal-400 uppercase tracking-wider m-0 mb-1.5">Execution Link</h4>
                        {run ? (
                          <div className="flex flex-col gap-1.5">
                            <div className="flex items-center justify-between text-[10px]">
                              <span className="text-white/60">Status:</span>
                              <span className="font-bold capitalize">{run.status}</span>
                            </div>
                            <div className="flex items-center justify-between text-[10px]">
                              <span className="text-white/60">Task Run:</span>
                              <span className="font-mono">{run.taskExecutionId ? run.taskExecutionId.slice(-8) : 'Pending'}</span>
                            </div>
                            {run.taskExecutionId && (
                              <button
                                onClick={() => {
                                  setActiveTaskIdWithRef(run.taskExecutionId);
                                  setIsTaskCompleted(false);
                                  // Switch to Tab 1
                                  setActiveTab('tactics');
                                  // Force poll fresh task details
                                  setTimeout(() => pollTasks(), 100);
                                }}
                                className="w-full mt-1.5 py-1.5 px-2.5 rounded-lg bg-teal-500/10 border border-teal-500/20 text-teal-400 hover:bg-teal-500/20 transition-all text-[9px] font-bold uppercase tracking-wider cursor-pointer flex items-center justify-center gap-1"
                              >
                                <Cpu className="w-3 h-3" />
                                Drill Down in Tactician
                              </button>
                            )}
                          </div>
                        ) : (
                          <span className="text-[10px] text-white/30 italic">No run information available.</span>
                        )}
                      </div>

                      <div className="border-t border-white/5 pt-3">
                        <h4 className="text-[10px] font-bold text-purple-400 uppercase tracking-wider m-0 mb-1.5">Variable Context</h4>
                        <div className="space-y-1 text-[8px] font-mono text-purple-300 leading-normal">
                          <div><span className="text-white/40">Full output:</span><br/><code>{`{{tasks.${selectedWfTask.taskTemplateId}.output}}`}</code></div>
                          <div className="pt-1.5"><span className="text-white/40">JSON property:</span><br/><code>{`{{tasks.${selectedWfTask.taskTemplateId}.output.propertyName}}`}</code></div>
                        </div>
                      </div>
                    </div>
                  );
                })()
              ) : (
                <div className="flex-1 flex items-center justify-center text-xs text-white/30 italic py-8 text-center">
                  Click on any task card in the Center Flow to inspect its live status, variables mapping, or trigger low-level drills.
                </div>
              )}
            </div>

            {/* Live Logs / Terminal Panel */}
            <div className="glass-panel rounded-2xl p-5 flex flex-col h-[280px]">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-3">
                <Terminal className="w-5 h-5 text-teal-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Live Orchestration Logs</h2>
              </div>

              <div className="flex-1 overflow-y-auto space-y-1.5 bg-slate-950/40 p-3 border border-white/5 rounded-xl terminal-font text-[9px] text-teal-300 max-h-[220px]">
                {workflowLogs.length === 0 ? (
                  <div className="h-full flex items-center justify-center text-white/20 italic">
                    Waiting for workflow activity...
                  </div>
                ) : (
                  workflowLogs.map((log, idx) => (
                    <div key={idx} className="leading-normal break-all whitespace-pre-wrap">
                      {log}
                    </div>
                  ))
                )}
              </div>
            </div>
            </div>

          </div>

        </div>
      )}

      {/* Settings Tab Layout */}
      {activeTab === 'settings' && (
        <div className="grid grid-cols-1 xl:grid-cols-12 gap-6 items-start animate-fade-in w-full">
          {/* Left Column: Engine & Model Configs (cols: 5) */}
          <div className="xl:col-span-5 flex flex-col gap-6 w-full">
            <div className="glass-panel rounded-2xl p-6 flex flex-col">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-4">
                <Settings className="w-5 h-5 text-teal-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Engine Configuration</h2>
              </div>

              <form onSubmit={handleSaveConfig} className="space-y-6">
                <div className="space-y-1.5">
                  <label className="text-xs font-bold text-white/70">Execution Mode</label>
                  <select 
                    value={config.modelMode}
                    onChange={(e) => setConfig({ ...config, modelMode: e.target.value })}
                    className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none focus:border-teal-500/50"
                  >
                    <option value="cooperative">Cooperative Hybrid (Local + Cloud)</option>
                    <option value="local">Local Tactician (Offline First)</option>
                    <option value="cloud">Cloud Fallback Only</option>
                  </select>
                  <span className="text-[10px] text-white/40 block leading-normal">
                    Hybrid compiles schemas in cloud and executes tool steps locally.
                  </span>
                </div>

                <div className="space-y-4 border-t border-white/5 pt-4">
                  <h3 className="text-xs font-bold text-purple-400 uppercase tracking-wider m-0">Cloud Strategist</h3>
                  <div className="grid grid-cols-2 gap-4">
                    <div className="space-y-1.5">
                      <label className="text-xs font-bold text-white/70">Provider</label>
                      <select 
                        value={config.cloudProvider}
                        onChange={(e) => setConfig({ ...config, cloudProvider: e.target.value })}
                        className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none focus:border-teal-500/50"
                      >
                        <option value="google">Google Gemini (Default)</option>
                        <option value="openai">OpenAI</option>
                      </select>
                    </div>
                    <div className="space-y-1.5">
                      <label className="text-xs font-bold text-white/70">API Key</label>
                      <input 
                        type="password" 
                        value={config.cloudApiKey}
                        onChange={(e) => setConfig({ ...config, cloudApiKey: e.target.value })}
                        placeholder="Enter API key..." 
                        className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none"
                      />
                    </div>
                  </div>
                </div>

                <div className={`space-y-4 border-t border-white/5 pt-4 transition-opacity duration-300 ${config.modelMode === 'cloud' ? 'opacity-40 pointer-events-none' : ''}`}>
                  <h3 className="text-xs font-bold text-teal-400 uppercase tracking-wider m-0">Local Sidecar (llama-server)</h3>

                  {config.modelMode !== 'cloud' && !models.some(m => m.id === selectedModelId && m.downloaded) && selectedModelId !== 'custom' && (
                    <div className="p-3.5 rounded-xl border border-amber-500/30 bg-amber-500/10 flex items-start gap-2.5">
                      <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0 mt-0.5" />
                      <div>
                        <p className="text-xs font-bold text-amber-300 m-0">Model download required</p>
                        <p className="text-[10px] text-amber-200/60 m-0 mt-1 leading-relaxed">
                          Download a model below to enable {config.modelMode === 'cooperative' ? 'Cooperative' : 'Local'} mode.
                        </p>
                      </div>
                    </div>
                  )}

                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Inference Model</label>
                    <select
                      value={selectedModelId}
                      onChange={(e) => {
                        const val = e.target.value;
                        setSelectedModelId(val);
                        if (val !== 'custom') {
                          const model = models.find(m => m.id === val);
                          if (model && model.downloaded) {
                            setConfig(prev => ({ ...prev, ggufModelPath: model.filename }));
                          }
                        }
                      }}
                      className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none focus:border-teal-500/50"
                    >
                      {models.map(m => (
                        <option key={m.id} value={m.id}>
                          {m.downloaded ? '✓ ' : ''}{m.displayName} {m.params ? `(${m.params})` : ''} — {m.sizeLabel}
                        </option>
                      ))}
                      <option value="custom">Custom HuggingFace URL...</option>
                    </select>
                  </div>

                  {selectedModelId === 'custom' && (
                    <div className="space-y-1.5">
                      <label className="text-xs font-bold text-white/70">Custom HuggingFace GGUF URL</label>
                      <input
                        type="url"
                        value={customModelUrl}
                        onChange={(e) => setCustomModelUrl(e.target.value)}
                        placeholder="https://huggingface.co/.../model.gguf"
                        className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none"
                      />
                    </div>
                  )}

                  {(() => {
                    const selectedModel = models.find(m => m.id === selectedModelId);
                    if (selectedModel && !selectedModel.downloaded) {
                      return (
                        <button
                          type="button"
                          onClick={() => handleDownloadModel(selectedModelId)}
                          disabled={isDownloading}
                          className="w-full py-2.5 rounded-xl bg-purple-600 hover:bg-purple-500 border border-purple-500/30 text-white text-xs font-bold transition-all cursor-pointer flex items-center justify-center gap-2"
                        >
                          {isDownloading ? <Loader2 className="w-4 h-4 animate-spin" /> : <Download className="w-4 h-4" />}
                          Download Model ({selectedModel.sizeLabel})
                        </button>
                      );
                    }
                    return null;
                  })()}

                  {sidecar.manifestProgress < 100 && sidecar.manifestProgress > 0 && (
                    <div className="p-4 rounded-xl border border-teal-500/20 bg-teal-500/5 space-y-2">
                      <div className="flex items-center justify-between text-xs font-bold text-teal-400">
                        <span>Downloading Model...</span>
                        <span>{sidecar.manifestProgress}%</span>
                      </div>
                      <div className="w-full h-1.5 bg-slate-900 rounded-full overflow-hidden">
                        <div className="h-full bg-teal-400 rounded-full" style={{ width: `${sidecar.manifestProgress}%` }} />
                      </div>
                    </div>
                  )}

                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Models Directory</label>
                    <input
                      type="text"
                      value={config.modelsDir}
                      onChange={(e) => setConfig({ ...config, modelsDir: e.target.value })}
                      placeholder="~/.tzro/models/"
                      className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white focus:outline-none"
                    />
                  </div>

                  <div className="space-y-2">
                    <div className="flex items-center justify-between text-xs font-bold text-white/70">
                      <span>Speed Floor (tokens/sec): {config.speedFloor.toFixed(1)}</span>
                    </div>
                    <input 
                      type="range" 
                      min="1" 
                      max="20" 
                      step="0.5"
                      value={config.speedFloor}
                      onChange={(e) => setConfig({ ...config, speedFloor: parseFloat(e.target.value) })}
                      className="w-full accent-teal-400 cursor-pointer"
                    />
                  </div>

                  <div className="space-y-3 pt-2">
                    <div className="flex gap-3">
                      <button type="button" onClick={startSidecarProcess} className="flex-1 py-2 rounded-xl bg-white/5 hover:bg-white/10 border border-white/10 text-white text-xs font-bold cursor-pointer">Start Sidecar</button>
                      <button type="button" onClick={stopSidecarProcess} className="flex-1 py-2 rounded-xl bg-white/5 hover:bg-white/10 border border-white/10 text-white text-xs font-bold cursor-pointer">Stop Sidecar</button>
                    </div>
                    <button type="button" onClick={clearContextCache} className="w-full py-2 rounded-xl bg-rose-500/10 hover:bg-rose-500/20 border border-rose-500/20 text-rose-300 text-xs font-bold flex items-center justify-center gap-1.5 cursor-pointer">
                      <Flame className="w-3.5 h-3.5" /> Erase Cache & Wipe Slots
                    </button>
                  </div>
                </div>

                <button 
                  type="submit"
                  disabled={config.modelMode !== 'cloud' && !models.some(m => m.id === selectedModelId && m.downloaded) && selectedModelId !== 'custom'}
                  className="w-full py-3 rounded-xl bg-teal-500 hover:bg-teal-400 text-slate-950 font-bold text-xs uppercase tracking-wider transition-all disabled:opacity-40 cursor-pointer"
                >
                  Save Configurations
                </button>
              </form>
            </div>
          </div>

          {/* Right Column: OpenAPI Integrations (cols: 7) */}
          <div className="xl:col-span-7 flex flex-col gap-6 w-full">
            {/* Form to add OpenAPI spec */}
            <div className="glass-panel rounded-2xl p-6">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-4">
                <Globe className="w-5 h-5 text-purple-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Add OpenAPI Dynamic Integration</h2>
              </div>

              {oaError && (
                <div className="p-3.5 rounded-xl border border-rose-500/30 bg-rose-500/10 text-xs text-rose-300 mb-4 leading-normal">
                  {oaError}
                </div>
              )}
              {oaSuccess && (
                <div className="p-3.5 rounded-xl border border-emerald-500/30 bg-emerald-500/10 text-xs text-emerald-300 mb-4 leading-normal">
                  {oaSuccess}
                </div>
              )}

              <form onSubmit={handleSaveOpenapiIntegration} className="space-y-4">
                <div className="grid grid-cols-2 gap-4">
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Integration ID (lowercase, alphanumeric)</label>
                    <input
                      type="text"
                      value={oaId}
                      onChange={(e) => setOaId(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                      placeholder="e.g. hubspot"
                      className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50"
                      required
                    />
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Integration Name</label>
                    <input
                      type="text"
                      value={oaName}
                      onChange={(e) => setOaName(e.target.value)}
                      placeholder="e.g. HubSpot Sales CRM"
                      className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50"
                      required
                    />
                  </div>
                </div>

                <div className="grid grid-cols-3 gap-4">
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Auth Type</label>
                    <select
                      value={oaAuthType}
                      onChange={(e) => setOaAuthType(e.target.value)}
                      className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none focus:border-teal-500/50"
                    >
                      <option value="none">No Auth</option>
                      <option value="bearer">Bearer Token</option>
                      <option value="apikey">Custom Header API Key</option>
                    </select>
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Header Key (for API Key only)</label>
                    <input
                      type="text"
                      value={oaAuthKey}
                      onChange={(e) => setOaAuthKey(e.target.value)}
                      placeholder="e.g. Authorization or Private-Token"
                      className={`w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50 ${oaAuthType !== 'apikey' ? 'opacity-45 pointer-events-none' : ''}`}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <label className="text-xs font-bold text-white/70">Token / API Key Value</label>
                    <input
                      type="password"
                      value={oaAuthValue}
                      onChange={(e) => setOaAuthValue(e.target.value)}
                      placeholder="Enter credential token..."
                      className={`w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50 ${oaAuthType === 'none' ? 'opacity-45 pointer-events-none' : ''}`}
                    />
                  </div>
                </div>

                <div className="space-y-1.5">
                  <label className="text-xs font-bold text-white/70">OpenAPI Spec (JSON Format)</label>
                  <textarea
                    value={oaSpec}
                    onChange={(e) => setOaSpec(e.target.value)}
                    placeholder='{ "openapi": "3.0.0", "servers": [{"url": "https://api.hubapi.com"}], "paths": { ... } }'
                    className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/20 focus:outline-none focus:border-teal-500/50 min-h-[140px] font-mono text-[10px] leading-normal"
                    required
                  />
                </div>

                <button
                  type="submit"
                  className="w-full py-2.5 rounded-xl bg-purple-600 hover:bg-purple-500 border border-purple-500/30 text-white text-xs font-bold transition-all cursor-pointer flex items-center justify-center gap-1.5"
                >
                  <Sparkles className="w-4 h-4" /> Register OpenAPI Spec
                </button>
              </form>
            </div>

            {/* List of active integrations */}
            <div className="glass-panel rounded-2xl p-6 flex flex-col min-h-[220px]">
              <div className="flex items-center gap-2 border-b border-white/5 pb-3 mb-4">
                <Database className="w-5 h-5 text-teal-400" />
                <h2 className="text-sm font-bold text-white uppercase tracking-wider m-0">Registered OpenAPI Specs ({openapiIntegrations.length})</h2>
              </div>

              <div className="space-y-3 max-h-[350px] overflow-y-auto pr-1">
                {openapiIntegrations.length === 0 ? (
                  <div className="h-full flex items-center justify-center text-xs text-white/30 italic py-8 text-center">
                    No custom OpenAPI specifications registered. Add one using the form above.
                  </div>
                ) : (
                  openapiIntegrations.map(oi => (
                    <div key={oi.id} className="p-4 rounded-xl border border-white/5 bg-white/2 hover:border-white/10 flex items-start justify-between gap-4 transition-all">
                      <div className="flex-1">
                        <div className="flex items-center gap-2">
                          <span className="text-[10px] font-bold font-mono px-2 py-0.5 rounded bg-purple-500/15 border border-purple-500/20 text-purple-300">
                            {oi.id}
                          </span>
                          <span className="font-bold text-xs text-white">{oi.name}</span>
                        </div>
                        <div className="flex items-center gap-3 text-[10px] text-white/40 mt-1.5">
                          <span>Auth Type: <span className="font-bold capitalize text-white/60">{oi.authType}</span></span>
                          <span>•</span>
                          <span>Registered: {new Date(oi.createdAt * 1000).toLocaleDateString()}</span>
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => handleDeleteOpenapiIntegration(oi.id)}
                        className="p-2 rounded bg-white/5 hover:bg-rose-500/20 text-rose-400 hover:text-rose-300 transition-all border border-white/5 cursor-pointer"
                        title="Delete spec and unregister all dynamic tools"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>
        </div>
      )}

        </div> {/* Closes Left Column */}

        {/* Right Column: Persistent Sticky Conversational System Console (cols: 4) */}
        <div className={`flex flex-col gap-6 w-full lg:h-full lg:min-h-0 ${isChatFullscreen ? 'lg:col-span-12' : 'lg:col-span-4'}`}>
          
          {/* Chat Console Panel */}
          <div className="glass-panel rounded-2xl p-5 flex flex-col h-full min-h-0">
            <div className="flex items-center justify-between border-b border-white/5 pb-3 mb-3">
              <div className="flex items-center gap-2">
                <Terminal className="w-5 h-5 text-teal-400" />
                <h2 className="text-base font-bold text-white leading-none">System Console</h2>
              </div>
              <button 
                onClick={() => setIsChatFullscreen(!isChatFullscreen)}
                className="p-1.5 rounded-lg border border-white/10 bg-white/5 hover:bg-white/10 text-white/70 hover:text-white transition-all cursor-pointer flex items-center justify-center"
                title={isChatFullscreen ? "Exit Fullscreen" : "Fullscreen Chat"}
              >
                {isChatFullscreen ? <Minimize2 className="w-4 h-4" /> : <Maximize2 className="w-4 h-4" />}
              </button>
            </div>
            
            <p className="text-xs text-white/50 leading-relaxed mb-3">
              Submit Natural Language instructions to route workflows, run jobs, or trigger scheduled agent heartbeats.
            </p>

            <div className="flex-1 overflow-y-auto space-y-3.5 pr-2 mb-4">
              {chatHistory.map((msg, i) => {
                if (msg.type === 'promotion') {
                  return (
                    <div key={i} className="glass-panel border-amber-500/30 bg-gradient-to-r from-amber-500/10 to-rose-500/10 rounded-xl p-4 my-2 border shadow-lg shadow-amber-950/10 animate-fade-in flex items-start gap-3">
                      <div className="p-2 rounded-lg bg-amber-500/20 text-amber-400 shrink-0">
                        <Sparkles className="w-4 h-4 animate-pulse" />
                      </div>
                      <div className="flex-1">
                        <h4 className="font-bold text-xs text-amber-300 flex items-center gap-1.5 uppercase tracking-wider m-0">
                          System Promotion Notice
                        </h4>
                        <p className="text-xs text-white/95 mt-1 leading-relaxed m-0 font-medium">{msg.text}</p>
                      </div>
                    </div>
                  );
                }

                if (msg.type === 'audit') {
                  return (
                    <div key={i} className="glass-panel border-emerald-500/20 bg-emerald-500/5 rounded-xl p-3.5 my-2 border shadow-lg shadow-emerald-950/10 animate-fade-in flex items-start gap-3">
                      <div className="p-2 rounded-lg bg-emerald-500/20 text-emerald-400 shrink-0">
                        <CheckCircle2 className="w-4 h-4" />
                      </div>
                      <div className="flex-1">
                        <h4 className="font-bold text-xs text-emerald-400 flex items-center gap-1.5 uppercase tracking-wider m-0">
                          Observer Verification Audit
                        </h4>
                        <p className="text-xs text-white/95 mt-1 leading-relaxed m-0 font-medium">{msg.text}</p>
                      </div>
                    </div>
                  );
                }

                return (
                  <div key={i} className={`flex flex-col text-xs rounded-xl p-3 border ${
                    msg.type === 'user' 
                      ? 'bg-purple-950/20 border-purple-500/20 self-end max-w-[85%]' 
                      : msg.type === 'agent' 
                        ? 'bg-teal-950/15 border-teal-500/15 max-w-[90%]' 
                        : 'bg-white/3 border-white/5 text-white/80'
                  }`}>
                    <div className="flex items-center justify-between gap-2 mb-1 opacity-60 font-medium">
                      <span className="text-[10px] text-teal-300 font-bold">[{msg.sender}]</span>
                      <span>{msg.time}</span>
                    </div>
                    <p className="text-white/90 break-words leading-relaxed m-0 flex items-center gap-1.5">
                      {msg.text}
                      {msg.isStreaming && (
                        <span className="inline-flex gap-0.5 items-center">
                          <span className="w-1.5 h-1.5 bg-teal-400 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
                          <span className="w-1.5 h-1.5 bg-teal-400 rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
                          <span className="w-1.5 h-1.5 bg-teal-400 rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
                        </span>
                      )}
                    </p>
                  </div>
                );
              })}
              <div ref={chatEndRef} />
            </div>

            <form onSubmit={handleChatSubmit} className="flex gap-2">
              <input 
                type="text" 
                value={chatInput}
                onChange={(e) => setChatInput(e.target.value)}
                placeholder="Type prompt (e.g. Migrate HubSpot contacts)..." 
                className="flex-1 bg-white/5 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50 transition-all"
                required
              />
              <button 
                type="submit" 
                className="px-4 rounded-xl bg-teal-500 text-slate-950 font-bold hover:bg-teal-400 transition-all flex items-center justify-center cursor-pointer shadow-lg shadow-teal-500/20"
              >
                <Send className="w-3.5 h-3.5" />
              </button>
            </form>
          </div>
        </div>

      </div> {/* Closes Global Split-Pane Container */}

      {/* Workflow Drawer backdrop scrim */}
      {isWfDrawerOpen && (
        <div
          className="drawer-backdrop"
          onClick={() => setIsWfDrawerOpen(false)}
        />
      )}

      {/* Sliding Workflow Editor Drawer Panel */}
      <div className={`fixed inset-y-0 right-0 z-50 w-full max-w-xl drawer-panel border-l border-white/10 shadow-2xl transition-transform duration-500 ease-in-out transform flex flex-col ${
        isWfDrawerOpen ? 'translate-x-0' : 'translate-x-full'
      }`}>
        <div className="flex items-center justify-between border-b border-white/10 p-5">
          <div className="flex items-center gap-2 text-white">
            <Workflow className="w-5 h-5 text-teal-400" />
            <h3 className="text-base font-bold">{editingWfId ? 'Edit Workflow' : 'Create New Workflow'}</h3>
          </div>
          <button 
            type="button"
            onClick={() => setIsWfDrawerOpen(false)}
            className="p-1 rounded-lg bg-white/5 hover:bg-white/10 border border-white/10 text-white/70 hover:text-white cursor-pointer"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        <form onSubmit={handleWorkflowSave} className="flex-1 overflow-y-auto p-5 space-y-5">
          
          {/* General info */}
          <div className="space-y-3.5">
            <h4 className="text-xs font-bold text-teal-400 uppercase tracking-wider border-b border-white/5 pb-1 m-0">General Parameters</h4>
            
            <div className="space-y-1.5">
              <label className="text-xs font-bold text-white/70">Workflow Name</label>
              <input 
                type="text" 
                value={wfName}
                onChange={(e) => setWfName(e.target.value)}
                placeholder="E.g. HubSpot Lead Sync Pipeline" 
                className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50"
                required
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-bold text-white/70">Description</label>
              <textarea 
                value={wfDesc}
                onChange={(e) => setWfDesc(e.target.value)}
                placeholder="Describe the business goal or stages of this pipeline..." 
                className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50 min-h-[60px]"
              />
            </div>
          </div>

          {/* Trigger Config */}
          <div className="space-y-3.5">
            <h4 className="text-xs font-bold text-teal-400 uppercase tracking-wider border-b border-white/5 pb-1 m-0">Trigger Schedule</h4>
            
            <div className="grid grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-white/70">Trigger Type</label>
                <select 
                  value={wfTriggerType}
                  onChange={(e) => setWfTriggerType(e.target.value)}
                  className="w-full bg-slate-900 border border-white/10 rounded-xl px-4 py-2.5 text-xs text-white focus:outline-none focus:border-teal-500/50"
                >
                  <option value="manual">Manual Trigger Only</option>
                  <option value="cron">Scheduled Cron Loop</option>
                </select>
              </div>

              {wfTriggerType === 'cron' && (
                <div className="space-y-1.5 animate-fade-in">
                  <label className="text-xs font-bold text-white/70">Cron Expression</label>
                  <input 
                    type="text" 
                    value={wfTriggerConfig}
                    onChange={(e) => setWfTriggerConfig(e.target.value)}
                    placeholder="E.g. */5 * * * * (every 5m)" 
                    className="w-full bg-white/5 border border-white/10 rounded-xl px-4 py-2 text-xs text-white placeholder-white/30 focus:outline-none"
                    required={wfTriggerType === 'cron'}
                  />
                  <span className="text-[9px] text-white/40 block leading-tight">
                    Standard 5-field cron (Min Hour Day Month DOW).
                  </span>
                </div>
              )}
            </div>
          </div>

          {/* Workflow Tasks List */}
          <div className="space-y-4">
            <h4 className="text-xs font-bold text-teal-400 uppercase tracking-wider border-b border-white/5 pb-1 m-0">
              Pipeline Nodes ({wfTasks.length})
            </h4>

            {/* List of current tasks */}
            <div className="space-y-2 max-h-[220px] overflow-y-auto pr-1">
              {wfTasks.length === 0 ? (
                <div className="p-4 rounded-xl border border-dashed border-white/10 text-center text-xs text-white/30 italic">
                  No tasks added to this workflow yet. Define your first task node below.
                </div>
              ) : (
                wfTasks.map((t) => (
                  <div key={t.taskTemplateId} className="p-3 rounded-xl border border-white/5 bg-white/2 flex items-start justify-between gap-3">
                    <div className="flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="text-[8px] font-mono bg-white/5 text-purple-300 px-1 py-0.2 rounded font-bold">
                          {t.taskTemplateId}
                        </span>
                        <span className="font-bold text-xs text-white">{t.name}</span>
                      </div>
                      <p className="text-[10px] text-white/60 mt-1 leading-normal m-0 line-clamp-2">
                        {t.instructions}
                      </p>
                      {t.dependencies && (
                        <div className="text-[8px] text-teal-400 font-mono mt-1">
                          depends on: {t.dependencies}
                        </div>
                      )}
                    </div>
                    <button
                      type="button"
                      onClick={() => handleRemoveWfTask(t.taskTemplateId)}
                      className="p-1 rounded hover:bg-rose-500/20 text-rose-400/80 hover:text-rose-400 cursor-pointer"
                      title="Remove task node"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </div>
                ))
              )}
            </div>

            {/* Add Task Sub-form */}
            <div className="p-4 rounded-xl border border-white/5 bg-slate-950/20 space-y-3">
              <h5 className="text-[10px] font-bold text-white uppercase tracking-wider m-0">Add Pipeline Task Node</h5>
              
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label className="text-[9px] font-bold text-white/50">Task Template ID (unique)</label>
                  <input
                    type="text"
                    value={drawerTaskId}
                    onChange={(e) => setDrawerTaskId(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                    placeholder="E.g. fetch_leads"
                    className="w-full bg-white/5 border border-white/10 rounded-lg px-2.5 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none"
                  />
                </div>
                <div className="space-y-1">
                  <label className="text-[9px] font-bold text-white/50">Task Name</label>
                  <input
                    type="text"
                    value={drawerTaskName}
                    onChange={(e) => setDrawerTaskName(e.target.value)}
                    placeholder="E.g. Salesforce Lead Query"
                    className="w-full bg-white/5 border border-white/10 rounded-lg px-2.5 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none"
                  />
                </div>
              </div>

              <div className="space-y-1">
                <label className="text-[9px] font-bold text-white/50">Natural Language Instructions</label>
                <textarea
                  value={drawerTaskInstructions}
                  onChange={(e) => setDrawerTaskInstructions(e.target.value)}
                  placeholder="E.g. Query all salesforce records modified today, return in JSON output format. Supports template interpolation: {{tasks.previous_id.output.property}}..."
                  className="w-full bg-white/5 border border-white/10 rounded-lg px-2.5 py-1.5 text-xs text-white placeholder-white/30 focus:outline-none focus:border-teal-500/50 min-h-[50px]"
                />
              </div>

              {/* Task Dependency Checkboxes */}
              {wfTasks.length > 0 && (
                <div className="space-y-1.5">
                  <label className="text-[9px] font-bold text-white/50 block">Dependencies (Prerequisites)</label>
                  <div className="flex flex-wrap gap-2 max-h-[80px] overflow-y-auto pr-1">
                    {wfTasks.map(t => {
                      const isChecked = drawerTaskDeps.includes(t.taskTemplateId);
                      return (
                        <label 
                          key={t.taskTemplateId}
                          className={`flex items-center gap-1.5 px-2.5 py-1 rounded-lg border text-[9px] font-mono cursor-pointer transition-all ${
                            isChecked
                              ? 'bg-teal-500/10 border-teal-500/30 text-teal-400 font-bold'
                              : 'bg-white/2 border-white/5 text-white/60 hover:bg-white/5'
                          }`}
                        >
                          <input
                            type="checkbox"
                            checked={isChecked}
                            onChange={() => {
                              if (isChecked) {
                                setDrawerTaskDeps(prev => prev.filter(d => d !== t.taskTemplateId));
                              } else {
                                setDrawerTaskDeps(prev => [...prev, t.taskTemplateId]);
                              }
                            }}
                            className="hidden"
                          />
                          {t.taskTemplateId}
                        </label>
                      );
                    })}
                  </div>
                </div>
              )}

              <button
                type="button"
                onClick={handleAddWfTask}
                className="w-full py-2 rounded-lg bg-teal-500/20 hover:bg-teal-500/30 border border-teal-500/30 text-teal-300 font-bold text-xs uppercase tracking-wider transition-all cursor-pointer flex items-center justify-center gap-1"
              >
                <Plus className="w-3.5 h-3.5" />
                Add Node to Pipeline
              </button>
            </div>
          </div>

          <button 
            type="submit"
            className="w-full py-3.5 rounded-xl bg-teal-500 hover:bg-teal-400 text-slate-950 font-bold text-xs uppercase tracking-wider transition-all cursor-pointer shadow-lg shadow-teal-500/20 mt-4"
          >
            {editingWfId ? 'Save Changes' : 'Create Workflow Pipeline'}
          </button>
        </form>
      </div>

      {/* Dynamic Proactive Notification Slide-Out Drawer */}
      {isNotifDrawerOpen && (
        <div className="fixed inset-0 z-50 flex justify-end">
          {/* Backdrop overlay */}
          <div 
            onClick={() => setIsNotifDrawerOpen(false)}
            className="absolute inset-0 bg-slate-950/60 backdrop-blur-sm transition-all"
          />

          {/* Drawer content (Glassmorphic) */}
          <div className="relative w-full max-w-md h-full bg-slate-900/90 border-l border-white/10 backdrop-blur-xl shadow-2xl flex flex-col z-10 transition-all duration-300 animate-slide-in text-white">
            {/* Drawer Header */}
            <div className="p-4 border-b border-white/10 flex items-center justify-between bg-slate-950/40">
              <div className="flex items-center gap-2">
                <Bell className="w-5 h-5 text-teal-400" />
                <h2 className="text-base font-black tracking-tight">Notification Center</h2>
                {notifications.filter(n => n.status === 'unread').length > 0 && (
                  <span className="px-2 py-0.5 rounded-full bg-rose-500/20 border border-rose-500/30 text-rose-400 text-[10px] font-black">
                    {notifications.filter(n => n.status === 'unread').length} new
                  </span>
                )}
              </div>
              <div className="flex items-center gap-2">
                {notifications.filter(n => n.status === 'unread').length > 0 && (
                  <button 
                    onClick={handleMarkAllRead}
                    className="text-[10px] uppercase font-bold text-teal-400 hover:text-teal-300 transition-all cursor-pointer bg-teal-500/5 hover:bg-teal-500/10 border border-teal-500/20 px-2 py-1 rounded-lg"
                  >
                    Read All
                  </button>
                )}
                <button 
                  onClick={() => setIsNotifDrawerOpen(false)}
                  className="p-1 rounded-lg hover:bg-white/5 text-white/50 hover:text-white transition-all cursor-pointer"
                >
                  <X className="w-5 h-5" />
                </button>
              </div>
            </div>

            {/* Notifications Feed */}
            <div className="flex-1 overflow-y-auto p-4 space-y-3 custom-scrollbar">
              {notifications.length === 0 ? (
                <div className="h-full flex flex-col items-center justify-center text-center text-white/30 space-y-2">
                  <Bell className="w-10 h-10 stroke-1" />
                  <p className="text-xs font-semibold">Inbox is clear</p>
                  <span className="text-[10px]">As background tasks and events trigger, alerts will appear durably here.</span>
                </div>
              ) : (
                notifications.map((n) => {
                  const isUnread = n.status === 'unread';
                  let typeColor: string;
                  let iconBg: string;
                  if (n.type === 'warning') {
                    typeColor = isUnread ? 'border-amber-500 border-l-4 bg-amber-500/5 hover:bg-amber-500/10' : 'border-white/5 bg-white/3 hover:bg-white/5 text-amber-400';
                    iconBg = 'bg-amber-500/10 text-amber-400';
                  } else if (n.type === 'error') {
                    typeColor = isUnread ? 'border-rose-500 border-l-4 bg-rose-500/5 hover:bg-rose-500/10' : 'border-white/5 bg-white/3 hover:bg-white/5 text-rose-400';
                    iconBg = 'bg-rose-500/10 text-rose-400';
                  } else if (n.type === 'action_required') {
                    typeColor = isUnread ? 'border-purple-500 border-l-4 bg-purple-500/5 hover:bg-purple-500/10' : 'border-white/5 bg-white/3 hover:bg-white/5 text-purple-400';
                    iconBg = 'bg-purple-500/10 text-purple-400';
                  } else {
                    typeColor = isUnread ? 'border-teal-500 border-l-4 bg-teal-500/5 hover:bg-teal-500/10' : 'border-white/5 bg-white/3 hover:bg-white/5 text-teal-400';
                    iconBg = 'bg-teal-500/10 text-teal-400';
                  }

                  let sourceLabel = n.source.toUpperCase();
                  if (n.source === 'executor') sourceLabel = 'Task Tactician';
                  if (n.source === 'observer') sourceLabel = 'Observer Agent';
                  if (n.source === 'workflow_orchestrator') sourceLabel = 'Workflows';
                  if (n.source === 'sidecar') sourceLabel = 'Llama-Server';

                  return (
                    <div 
                      key={n.id}
                      onClick={() => handleNotifClick(n)}
                      className={`group p-3.5 rounded-xl border transition-all duration-200 cursor-pointer relative hover:scale-[1.02] hover:shadow-lg ${typeColor}`}
                    >
                      <div className="flex items-start gap-3">
                        <div className={`p-2 rounded-lg ${iconBg} transition-all`}>
                          {n.source === 'executor' && <Terminal className="w-4 h-4" />}
                          {n.source === 'observer' && <Layers className="w-4 h-4" />}
                          {n.source === 'workflow_orchestrator' && <Workflow className="w-4 h-4" />}
                          {n.source === 'sidecar' && <Cpu className="w-4 h-4" />}
                          {n.source !== 'executor' && n.source !== 'observer' && n.source !== 'workflow_orchestrator' && n.source !== 'sidecar' && <Bell className="w-4 h-4" />}
                        </div>
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center justify-between gap-2">
                            <span className="text-[9px] font-black uppercase tracking-wider text-white/40">{sourceLabel}</span>
                            <span className="text-[9px] text-white/30">{new Date(n.createdAt * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                          </div>
                          <h4 className="font-bold text-xs text-white mt-1 group-hover:text-teal-300 transition-all">{n.title}</h4>
                          <p className="text-[10px] text-white/60 mt-1 leading-relaxed whitespace-pre-wrap">{n.message}</p>
                          
                          {/* Linking pills */}
                          {(n.taskId || n.workflowId || n.targetId) && (
                            <div className="flex flex-wrap gap-1.5 mt-2.5">
                              {n.taskId && (
                                <span className="px-2 py-0.5 rounded bg-white/5 border border-white/10 text-[8px] font-mono text-white/50">
                                  Task: {n.taskId.slice(-8)}
                                </span>
                              )}
                              {n.workflowId && (
                                <span className="px-2 py-0.5 rounded bg-white/5 border border-white/10 text-[8px] font-mono text-white/50">
                                  Workflow: {n.workflowId.slice(-8)}
                                </span>
                              )}
                              {n.targetId && (
                                <span className="px-2 py-0.5 rounded bg-teal-500/10 border border-teal-500/20 text-[8px] font-mono text-teal-300">
                                  Target: {n.targetId}
                                </span>
                              )}
                            </div>
                          )}
                        </div>
                      </div>

                      {/* Quick checkmark dismiss button */}
                      {isUnread && (
                        <button 
                          onClick={(e) => {
                            e.stopPropagation();
                            handleMarkRead(n.id, 'read');
                          }}
                          className="absolute bottom-3 right-3 p-1.5 rounded-lg border border-white/5 hover:border-teal-500/30 hover:bg-teal-500/10 text-white/20 hover:text-teal-400 transition-all cursor-pointer opacity-0 group-hover:opacity-100"
                          title="Dismiss notification"
                        >
                          <Check className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          </div>
        </div>
      )}

    </div>
  );
}
