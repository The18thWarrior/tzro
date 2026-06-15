import React from 'react';
import { 
  Stack, Grid, Section, MetricCard, TaskTable, EventFeed, 
  ConfigPanel, SidecarStatus, NotificationList, WorkflowMonitor, 
  TaskSpotlight, WorkflowSpotlight, Annotation 
} from '../components/Primitives';

export interface RenderContext {
  tasks: any[];
  events: any[];
  config: any;
  sidecar: any;
  downloadedModels: any[];
  notifications: any[];
  workflows: any[];
  selectedTaskId: string | undefined;
  onSelectTask: (taskId: string) => void;
  selectedWorkflowExecution: any;
  selectedWorkflowTasks: any[];
  onTriggerWorkflow: (wfId: string) => void;
  onRefreshNotifications: () => void;
  selectedTaskDetails: any; // For full DAG graph view
}

export const renderLayoutNode = (node: any, ctx: RenderContext): React.ReactNode => {
  if (!node || !node.type) return null;

  const type = node.type;
  const props = node.props || {};
  const children = node.children || [];

  switch (type) {
    // Layouts
    case 'Stack':
      return (
        <Stack key={node.id || Math.random().toString()} gap={props.gap}>
          {children.map((child: any) => renderLayoutNode(child, ctx))}
        </Stack>
      );
    case 'Grid':
      return (
        <Grid key={node.id || Math.random().toString()} columns={node.columns || 3}>
          {children.map((child: any) => renderLayoutNode(child, ctx))}
        </Grid>
      );
    case 'Section':
      return (
        <Section key={node.id || Math.random().toString()} title={props.title || "Section"} subtitle={props.subtitle}>
          {children.map((child: any) => renderLayoutNode(child, ctx))}
        </Section>
      );

    // Static components
    case 'MetricCard':
      return (
        <MetricCard 
          key={node.id || Math.random().toString()} 
          label={props.label} 
          value={props.value} 
          trend={props.trend} 
          trendValue={props.trendValue} 
        />
      );
    case 'ConfigPanel':
      return (
        <ConfigPanel 
          key={node.id || Math.random().toString()} 
          config={ctx.config} 
          downloadedModels={ctx.downloadedModels} 
        />
      );
    case 'Annotation':
      return (
        <Annotation 
          key={node.id || Math.random().toString()} 
          type={props.type} 
          message={props.message || ""} 
        />
      );

    // Live components
    case 'TaskTable':
      return (
        <TaskTable 
          key={node.id || Math.random().toString()} 
          tasks={ctx.tasks} 
          onSelectTask={ctx.onSelectTask} 
          selectedTaskId={ctx.selectedTaskId} 
        />
      );
    case 'EventFeed':
      return (
        <EventFeed 
          key={node.id || Math.random().toString()} 
          events={ctx.events} 
        />
      );
    case 'SidecarStatus':
      return (
        <SidecarStatus 
          key={node.id || Math.random().toString()} 
          sidecar={ctx.sidecar} 
        />
      );
    case 'NotificationList':
      return (
        <NotificationList 
          key={node.id || Math.random().toString()} 
          notifications={ctx.notifications} 
          onRefresh={ctx.onRefreshNotifications} 
        />
      );
    case 'WorkflowMonitor':
      return (
        <WorkflowMonitor 
          key={node.id || Math.random().toString()} 
          workflows={ctx.workflows} 
          onTrigger={ctx.onTriggerWorkflow} 
        />
      );
    case 'TaskSpotlight':
      return (
        <TaskSpotlight 
          key={node.id || Math.random().toString()} 
          task={ctx.selectedTaskDetails} 
        />
      );
    case 'WorkflowSpotlight':
      return (
        <WorkflowSpotlight 
          key={node.id || Math.random().toString()} 
          execution={ctx.selectedWorkflowExecution} 
          tasks={ctx.selectedWorkflowTasks} 
        />
      );

    default:
      return (
        <div key={node.id || Math.random().toString()} className="p-4 border border-red-500 bg-red-500/10 text-red-400 rounded">
          Unknown component type: {type}
        </div>
      );
  }
};
