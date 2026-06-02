package tui

import (
	"tzro/internal/stream"

	"github.com/charmbracelet/bubbletea"
)

// StreamMsg wraps real-time stream chunks pushed from the StreamBus.
type StreamMsg stream.StreamChunk

// listenOnChannel blocks on the given channel and returns a StreamMsg.
func listenOnChannel(ch <-chan stream.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return nil
		}
		return StreamMsg(chunk)
	}
}
