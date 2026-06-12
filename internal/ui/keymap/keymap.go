// Package keymap defines tubeamp's key bindings (built on bubbles/key) together
// with the human-readable help text rendered in the help overlay and the
// context hint bar. It is the single source of truth for which keys do what.
package keymap

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds every binding used by the UI. Bindings carry both the matched
// keys and the help text (key glyph + description) shown to the user.
type KeyMap struct {
	// Focus / navigation.
	Focus1    key.Binding
	Focus2    key.Binding
	Focus3    key.Binding
	Focus4    key.Binding
	FocusPrev key.Binding
	FocusNext key.Binding
	Up        key.Binding
	Down      key.Binding
	Top       key.Binding
	Bottom    key.Binding

	// Activation / playback.
	Enter    key.Binding
	Space    key.Binding
	Next     key.Binding
	Prev     key.Binding
	SeekBack key.Binding
	SeekFwd  key.Binding
	VolUp    key.Binding
	VolDown  key.Binding
	Mute     key.Binding

	// Queue / main-view actions.
	Append     key.Binding
	InsertNext key.Binding
	Open       key.Binding
	Remove     key.Binding
	MoveUp     key.Binding
	MoveDown   key.Binding
	ClearQueue key.Binding

	// Application.
	Search key.Binding
	Theme  key.Binding
	Help   key.Binding
	Quit   key.Binding
	Esc    key.Binding
}

// Default returns the standard tubeamp key bindings.
func Default() KeyMap {
	return KeyMap{
		Focus1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "library")),
		Focus2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "playlists")),
		Focus3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "queue")),
		Focus4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "main")),
		FocusPrev: key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "previous panel")),
		FocusNext: key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "next panel")),
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Top:       key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:    key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),

		Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "play / select")),
		// Bubble Tea reports the space key's String() as a single space.
		Space:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "pause")),
		Next:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next track")),
		Prev:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "previous track")),
		SeekBack: key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "seek -5s")),
		SeekFwd:  key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "seek +5s")),
		VolUp:    key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+", "volume up")),
		VolDown:  key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "volume down")),
		Mute:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mute")),

		Append:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "append to queue")),
		InsertNext: key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "play next")),
		Open:       key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open album")),
		Remove:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "remove")),
		MoveUp:     key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "move up")),
		MoveDown:   key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "move down")),
		ClearQueue: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear queue")),

		Search: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Theme:  key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "theme")),
		Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Esc:    key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back / close")),
	}
}

// FullHelp returns the bindings grouped for display in the help overlay. The
// group order matches HelpGroupTitles.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Focus1, k.Focus2, k.Focus3, k.Focus4, k.FocusPrev, k.FocusNext, k.Up, k.Down, k.Top, k.Bottom},
		{k.Enter, k.Space, k.Next, k.Prev, k.SeekBack, k.SeekFwd, k.VolUp, k.VolDown, k.Mute},
		{k.Append, k.InsertNext, k.Open, k.Remove, k.MoveUp, k.MoveDown, k.ClearQueue},
		{k.Search, k.Theme, k.Help, k.Quit, k.Esc},
	}
}

// HelpGroupTitles labels the groups returned by FullHelp.
func HelpGroupTitles() []string {
	return []string{"Navigation", "Playback", "Queue & Main", "App"}
}
