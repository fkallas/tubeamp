package overlay

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fkallas/tubeamp/internal/theme"
	"github.com/fkallas/tubeamp/internal/ui/panels"
)

// Search is the search-query overlay wrapping a bubbles text input.
type Search struct {
	input textinput.Model
}

// NewSearch returns a focused search overlay with an empty query.
func NewSearch() Search {
	ti := textinput.New()
	ti.Placeholder = "song or artist…"
	ti.Prompt = "› "
	ti.CharLimit = 128
	ti.Width = 40
	ti.Focus()
	return Search{input: ti}
}

// Update feeds a message to the underlying text input and returns its command.
func (s *Search) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// Value returns the current query text.
func (s Search) Value() string { return s.input.Value() }

// Reset clears the query and re-focuses the input.
func (s *Search) Reset() {
	s.input.Reset()
	s.input.Focus()
}

// SetWidth adjusts the input field width.
func (s *Search) SetWidth(w int) {
	if w < 4 {
		w = 4
	}
	s.input.Width = w
}

// Focus focuses the input, returning any cursor-blink command.
func (s *Search) Focus() tea.Cmd { return s.input.Focus() }

// View renders the search overlay box sized to fit within maxW×maxH.
func (s Search) View(th *theme.Theme, maxW, maxH int) string {
	w := 54
	if w > maxW-4 {
		w = maxW - 4
	}
	body := []string{
		th.Muted().Render("Search YouTube Music"),
		"",
		s.input.View(),
	}
	return panels.Box(th, "Search", body, w, len(body)+2, true)
}
