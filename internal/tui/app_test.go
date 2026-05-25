package tui

import (
	"context"
	"errors"
	"testing"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/stream"

	"github.com/charmbracelet/bubbletea"
)

type mockClient struct{}

func (m mockClient) GetTasks() ([]TaskStateItem, error) {
	return nil, nil
}

func (m mockClient) GetMemories() (MemoryPayload, error) {
	return MemoryPayload{}, nil
}

func (m mockClient) AddMemory(userId, memType, content, context string, confidence float64) error {
	return nil
}

func (m mockClient) GetSkills() ([]memory.Skill, error) {
	return nil, nil
}

func (m mockClient) GetMCPList() (map[string]mcp.MCPServerConfig, error) {
	return nil, nil
}

func (m mockClient) GetSidecarStatus() (SidecarStatus, error) {
	return SidecarStatus{Status: "Inactive"}, nil
}

func (m mockClient) GetNotifications() ([]memory.DurableNotification, error) {
	return nil, nil
}

func (m mockClient) GetEventsStream(ctx context.Context) (<-chan stream.StreamChunk, error) {
	return nil, errors.New("events stream unsupported in mock")
}

func (m mockClient) TriggerWorkflow(workflowId string) error {
	return nil
}

func (m mockClient) ToggleWorkflow(workflowId string) error {
	return nil
}

func TestTUI_InitialState(t *testing.T) {
	client := mockClient{}
	model := NewModel(client)

	if model.ActivePanel != DashboardPanel {
		t.Errorf("expected initial panel DashboardPanel (0), got: %v", model.ActivePanel)
	}
	if model.SidebarIndex != 0 {
		t.Errorf("expected initial sidebar index 0, got: %d", model.SidebarIndex)
	}
	if model.Quitting {
		t.Error("expected Quitting to be false, got true")
	}
}

func TestTUI_NavigationLoop(t *testing.T) {
	client := mockClient{}
	m := NewModel(client)

	// Send keypress Msg: down arrow
	msg := tea.KeyMsg{Type: tea.KeyDown, Runes: []rune{}}
	resModel, cmd := m.Update(msg)
	
	newModel, ok := resModel.(Model)
	if !ok {
		t.Fatalf("expected tea.Model to cast back to tui.Model, got: %T", resModel)
	}
	if cmd != nil {
		t.Error("expected arrow key down to return nil command, got non-nil")
	}

	// SidebarIndex should move down to 1
	if newModel.SidebarIndex != 1 {
		t.Errorf("expected SidebarIndex to increment to 1, got: %d", newModel.SidebarIndex)
	}

	// Send enter keypress to select LogsPanel (index 1)
	msgEnter := tea.KeyMsg{Type: tea.KeyEnter, Runes: []rune{}}
	resModel2, _ := newModel.Update(msgEnter)
	newModel2 := resModel2.(Model)

	if newModel2.ActivePanel != LogsPanel {
		t.Errorf("expected active panel to switch to LogsPanel (1) on enter, got: %v", newModel2.ActivePanel)
	}

	// Send up arrow keypress
	msgUp := tea.KeyMsg{Type: tea.KeyUp, Runes: []rune{}}
	resModel3, _ := newModel2.Update(msgUp)
	newModel3 := resModel3.(Model)

	if newModel3.SidebarIndex != 0 {
		t.Errorf("expected SidebarIndex to decrement to 0, got: %d", newModel3.SidebarIndex)
	}

	// Send 'q' key to quit
	msgQuit := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	resModel4, _ := newModel3.Update(msgQuit)
	newModel4 := resModel4.(Model)

	if !newModel4.Quitting {
		t.Error("expected quitting state to be true on 'q' keypress, got false")
	}
}
