package ui

import (
	tea "charm.land/bubbletea/v2"
)

// Modal is the interface for modal dialogs.
// The Update method returns the updated modal, a command, and a bool indicating if the modal should close.
type Modal interface {
	Update(msg tea.Msg, keys keyMap) (Modal, tea.Cmd, bool)
	View(theme Theme, width, height int) string
}
