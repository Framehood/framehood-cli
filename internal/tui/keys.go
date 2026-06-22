package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap centralizes every studio key binding. Keeping them as data (rather
// than string literals scattered through Update) means the help bar and the
// matching logic can never drift apart, and later steps can rebind without
// hunting.
type keyMap struct {
	// Palette
	SlashOpen    key.Binding // / — open slash-command palette (from empty input)
	PaletteUp    key.Binding // ↑ — navigate grid
	PaletteDown  key.Binding // ↓
	PaletteLeft  key.Binding // ←
	PaletteRight key.Binding // →

	// Input / generation
	Generate key.Binding // enter — submit active action
	Esc      key.Binding // esc — close palette / cancel form

	// Action selector (palette-closed): cycle the work-action ring.
	ShiftTab key.Binding // shift+tab — next work action
	Tab      key.Binding // tab — previous work action

	// Input-compose history recall (palette closed, input focused).
	HistPrev key.Binding // ↑ — recall older submitted prompt
	HistNext key.Binding // ↓ — recall newer / restore draft

	// Output quick-keys (history pane)
	Up   key.Binding // ↑/k — move history selection
	Down key.Binding // ↓/j
	Open key.Binding // o — open selected result in browser
	Copy key.Binding // c — copy URL to clipboard
	Save key.Binding // s — save to disk
	Use  key.Binding // u — chain result into next action

	// Global
	Help      key.Binding // ? — toggle full help
	ForceQuit key.Binding // ctrl+c — quit
}

func defaultKeys() keyMap {
	return keyMap{
		SlashOpen:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
		PaletteUp:    key.NewBinding(key.WithKeys("up")),
		PaletteDown:  key.NewBinding(key.WithKeys("down")),
		PaletteLeft:  key.NewBinding(key.WithKeys("left")),
		PaletteRight: key.NewBinding(key.WithKeys("right")),

		Generate: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "generate")),
		Esc:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close/cancel")),

		ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇧⇥", "next action")),
		Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "prev action")),

		HistPrev: key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "history")),
		HistNext: key.NewBinding(key.WithKeys("down")),

		Up:   key.NewBinding(key.WithKeys("up", "k")),
		Down: key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↑↓", "select")),
		Open: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open")),
		Copy: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy url")),
		Save: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save")),
		Use:  key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "use as input")),

		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	}
}

// helpContext adapts the focus-aware key set to the bubbles/help.KeyMap
// interface, so the help bar always lists exactly the keys live in the current
// state.
type helpContext struct {
	keys            keyMap
	focus           focusZone
	paletteOpen     bool
	working         bool
	hasResult       bool // the selected row has an openable result URL
	hasRows         bool // the history table has at least one row
	hasInputHistory bool // the compose box has recallable prompts (↑/↓)
}

func (h helpContext) ShortHelp() []key.Binding {
	k := h.keys
	if h.working {
		return []key.Binding{k.ForceQuit}
	}
	if h.paletteOpen {
		return []key.Binding{k.Generate, k.Esc, k.ForceQuit}
	}
	switch h.focus {
	case zoneOutput:
		b := make([]key.Binding, 0, 8)
		if h.hasRows {
			b = append(b, k.Down)
		}
		if h.hasResult {
			b = append(b, k.Open, k.Copy, k.Save, k.Use)
		}
		return append(b, k.Help, k.ForceQuit)
	default: // zoneInput (primary surface)
		b := []key.Binding{k.SlashOpen, k.ShiftTab, k.Tab}
		if h.hasInputHistory {
			b = append(b, k.HistPrev) // ↑↓ history
		}
		return append(b, k.Generate, k.Help, k.ForceQuit)
	}
}

// FullHelp lists every binding grouped — shown when `?` is toggled.
func (h helpContext) FullHelp() [][]key.Binding {
	k := h.keys
	compose := []key.Binding{k.SlashOpen, k.ShiftTab, k.Tab}
	if h.hasInputHistory {
		compose = append(compose, k.HistPrev)
	}
	compose = append(compose, k.Generate, k.Esc)
	return [][]key.Binding{
		compose,
		{k.Down, k.Open, k.Copy, k.Save, k.Use},
		{k.Help, k.ForceQuit},
	}
}
