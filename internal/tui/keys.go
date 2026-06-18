package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap centralizes every studio key binding. Keeping them as data (rather than
// string literals scattered through Update) means the help bar and the matching
// logic can never drift apart, and later steps can rebind without hunting.
type keyMap struct {
	Tab       key.Binding
	ShiftTab  key.Binding
	Left      key.Binding
	Right     key.Binding
	Write     key.Binding // enter, in the tabs zone → go write the prompt
	Generate  key.Binding // enter, in the input zone → submit
	New       key.Binding // enter, in the output zone → start over
	Open      key.Binding // o, in the output zone → open result in browser
	Esc       key.Binding
	Quit      key.Binding
	Help      key.Binding
	ForceQuit key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Tab:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "pane")),
		ShiftTab:  key.NewBinding(key.WithKeys("shift+tab")),
		Left:      key.NewBinding(key.WithKeys("left", "h")),
		Right:     key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("←/→", "switch type")),
		Write:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "write prompt")),
		Generate:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "generate")),
		New:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "new")),
		Open:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser")),
		Esc:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "leave field")),
		Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more")),
		ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}

// helpContext adapts the focus-aware key set to the bubbles/help.KeyMap
// interface, so the help bar always lists exactly the keys live in the current
// pane (and `?` expands to the full set).
type helpContext struct {
	keys      keyMap
	focus     focusZone
	working   bool
	hasResult bool
}

func (h helpContext) ShortHelp() []key.Binding {
	k := h.keys
	if h.working {
		return []key.Binding{k.Tab, k.ForceQuit}
	}
	switch h.focus {
	case zoneTabs:
		return []key.Binding{k.Right, k.Write, k.Tab, k.Help, k.Quit}
	case zoneOutput:
		b := make([]key.Binding, 0, 5)
		if h.hasResult {
			b = append(b, k.Open)
		}
		return append(b, k.New, k.Tab, k.Help, k.Quit)
	default: // zoneInput — q and ? are typed into the prompt here, so the bar
		// must not advertise them as commands.
		return []key.Binding{k.Generate, k.Esc, k.Tab}
	}
}

// FullHelp lists every binding grouped by pane — shown when `?` is toggled.
func (h helpContext) FullHelp() [][]key.Binding {
	k := h.keys
	return [][]key.Binding{
		{k.Right, k.Write},           // tabs
		{k.Generate, k.Esc},          // input
		{k.Open, k.New},              // output
		{k.Tab, k.Quit, k.ForceQuit}, // global
	}
}
