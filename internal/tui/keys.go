package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap centralizes every studio key binding. Keeping them as data (rather than
// string literals scattered through Update) means the help bar and the matching
// logic can never drift apart, and later steps can rebind without hunting.
type keyMap struct {
	Tab       key.Binding
	ShiftTab  key.Binding
	Write     key.Binding // enter, in the tabs zone → go write the prompt
	Generate  key.Binding // enter, in the input zone → submit
	New       key.Binding // enter, in the output zone → start over
	Open      key.Binding // o, in the output zone → open selected result in browser
	Copy      key.Binding // c, in the output zone → copy selected URL to clipboard
	Save      key.Binding // s, in the output zone → download selected result
	Use       key.Binding // u, in the output zone → chain the result into a new action
	Palette   key.Binding // :, jump to the action filter from anywhere non-typing
	Up        key.Binding // k/↑, move the history selection
	Down      key.Binding // j/↓, move the history selection
	Esc       key.Binding
	Quit      key.Binding
	Help      key.Binding
	ForceQuit key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Tab:       key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "pane")),
		ShiftTab:  key.NewBinding(key.WithKeys("shift+tab")),
		Write:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "write prompt")),
		Generate:  key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "generate")),
		New:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "new")),
		Open:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
		Copy:      key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy url")),
		Save:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
		Use:       key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "use as input")),
		Palette:   key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "find action")),
		Up:        key.NewBinding(key.WithKeys("up", "k")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↑↓", "select")),
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
	hasResult bool // the selected row has an openable result URL
	hasRows   bool // the history table has at least one row
}

func (h helpContext) ShortHelp() []key.Binding {
	k := h.keys
	if h.working {
		return []key.Binding{k.Tab, k.ForceQuit}
	}
	switch h.focus {
	case zoneTabs:
		return []key.Binding{k.Write, k.Palette, k.Tab, k.Help, k.Quit}
	case zoneOutput:
		b := make([]key.Binding, 0, 9)
		if h.hasRows {
			b = append(b, k.Down) // ↑↓ select
		}
		if h.hasResult {
			b = append(b, k.Open, k.Copy, k.Save, k.Use)
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
		{k.Write, k.Palette},                    // tabs (nav)
		{k.Generate, k.Esc},                     // input
		{k.Down, k.Open, k.Copy, k.Save, k.Use}, // output
		{k.Palette, k.New, k.Tab, k.Quit, k.ForceQuit},
	}
}
