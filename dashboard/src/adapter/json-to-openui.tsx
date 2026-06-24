import React from 'react';
import { 
  Stack, Grid, Section, MetricCard, EventFeed, 
  ConfigPanel, SidecarStatus, NotificationList, WorkflowMonitor, 
  TaskTableWithSpotlight, WorkflowSpotlight, Annotation 
} from '../components';

export interface RenderContext {
  tasks: any[];
  events: any[];
  config: any;
  sidecar: any;
  downloadedModels: any[];
  notifications: any[];
  workflows: any[];
  workflowExecutions: any[];
  selectedTaskId: string | undefined;
  onSelectTask: (taskId: string) => void;
  onCloseSpotlight: () => void;
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
        <TaskTableWithSpotlight 
          key={node.id || Math.random().toString()} 
          tasks={ctx.tasks} 
          onSelectTask={ctx.onSelectTask} 
          selectedTaskId={ctx.selectedTaskId}
          selectedTaskDetails={ctx.selectedTaskDetails}
          onCloseSpotlight={ctx.onCloseSpotlight}
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
          executions={ctx.workflowExecutions}
          onTrigger={ctx.onTriggerWorkflow} 
        />
      );
    case 'TaskSpotlight':
      // Standalone TaskSpotlight nodes are now absorbed into TaskTable's side panel.
      // Render nothing — the composite TaskTableWithSpotlight handles it.
      return null;
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

// Check if a component type exists anywhere in the layout tree
const layoutContains = (node: any, targetType: string): boolean => {
  if (!node) return false;
  if (node.type === targetType) return true;
  const children = node.children || [];
  return children.some((child: any) => layoutContains(child, targetType));
};

/**
 * Renders the layout tree with automatic fallback injection.
 * If the LLM-generated spec omits TaskTable or WorkflowMonitor,
 * they are appended as a fallback section to ensure visibility.
 */
export const renderLayoutWithFallback = (layout: any, ctx: RenderContext): React.ReactNode => {
  const mainContent = renderLayoutNode(layout, ctx);

  // Determine which critical components are missing from the layout
  const hasTaskTable = layoutContains(layout, 'TaskTable');
  const hasWorkflowMonitor = layoutContains(layout, 'WorkflowMonitor');

  const fallbacks: React.ReactNode[] = [];

  if (!hasTaskTable && ctx.tasks.length > 0) {
    fallbacks.push(
      <Section key="fallback-tasks" title="Recent Tasks" subtitle="Auto-included — not in generated layout">
        <TaskTableWithSpotlight
          tasks={ctx.tasks}
          onSelectTask={ctx.onSelectTask}
          selectedTaskId={ctx.selectedTaskId}
          selectedTaskDetails={ctx.selectedTaskDetails}
          onCloseSpotlight={ctx.onCloseSpotlight}
        />
      </Section>
    );
  }

  if (!hasWorkflowMonitor && (ctx.workflows.length > 0 || ctx.workflowExecutions.length > 0)) {
    fallbacks.push(
      <Section key="fallback-workflows" title="Workflow Runs" subtitle="Auto-included — not in generated layout">
        <WorkflowMonitor workflows={ctx.workflows} executions={ctx.workflowExecutions} onTrigger={ctx.onTriggerWorkflow} />
      </Section>
    );
  }

  if (fallbacks.length === 0) return mainContent;

  return (
    <>
      {mainContent}
      {fallbacks}
    </>
  );
};
