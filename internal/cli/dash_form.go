package cli

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// dashField is one line of a dashForm: text, a choice toggled with space,
// or a pick from a list.
type dashField struct {
	label string
	// hint is shown faint after an empty text field, beside a toggle, or in
	// place of a pick with nothing to pick from.
	hint   string
	value  []rune
	cursor int
	// toggle makes the field a yes/no choice; on is its value.
	toggle bool
	on     bool
	// choices make the field a pick: ← and → move to one, space picks it or
	// drops it. picked is by index; single allows only one.
	choices []string
	picked  []bool
	at      int
	single  bool
}

// text is what was typed, or the picked choices in order, blank-separated.
func (f dashField) text() string {
	if f.choices != nil {
		var out []string
		for i, c := range f.choices {
			if f.picked[i] {
				out = append(out, c)
			}
		}
		return strings.Join(out, " ")
	}
	return strings.TrimSpace(string(f.value))
}

// pick makes f a pick from choices, which may be none.
func (f dashField) pick(choices []string, single bool) dashField {
	f.choices, f.picked, f.single = append([]string{}, choices...), make([]bool, len(choices)), single
	return f
}

// dashForm is a few fields typed in below the table, for the actions that
// need more than a y. Keys go to it while it is open; enter submits it, esc
// cancels it. It is a line editor, not a widget library: dash needs no more.
type dashForm struct {
	title  string
	fields []dashField
	focus  int
	// check, run on every change, returns what is wrong with the form, which
	// is shown and blocks enter, or "".
	check func(f *dashForm) string
	// submit is the action, once check passes.
	submit func(f *dashForm) tea.Cmd
	// note is what check last said; busy, the note line while submit runs.
	note, busy string
}

// field is the field labelled label.
func (f *dashForm) field(label string) *dashField {
	for i := range f.fields {
		if f.fields[i].label == label {
			return &f.fields[i]
		}
	}
	return nil
}

func (f *dashForm) recheck() {
	f.note = ""
	if f.check != nil {
		f.note = f.check(f)
	}
}

// key handles one key. It returns the action to run when the form is
// submitted, and whether the form is finished, submitted or cancelled.
func (f *dashForm) key(msg tea.KeyMsg) (tea.Cmd, bool) {
	cur := &f.fields[f.focus]
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		return nil, true
	case tea.KeyEnter:
		if f.recheck(); f.note != "" {
			return nil, false
		}
		return f.submit(f), true
	case tea.KeyTab, tea.KeyDown:
		f.focus = (f.focus + 1) % len(f.fields)
		return nil, false
	case tea.KeyShiftTab, tea.KeyUp:
		f.focus = (f.focus + len(f.fields) - 1) % len(f.fields)
		return nil, false
	}
	if cur.toggle {
		if msg.Type == tea.KeySpace || msg.String() == " " || msg.String() == "x" {
			cur.on = !cur.on
			f.recheck()
		}
		return nil, false
	}
	if cur.choices != nil {
		switch {
		case len(cur.choices) == 0:
		case msg.Type == tea.KeyLeft:
			cur.at = max(cur.at-1, 0)
		case msg.Type == tea.KeyRight:
			cur.at = min(cur.at+1, len(cur.choices)-1)
		case msg.Type == tea.KeySpace || msg.String() == " " || msg.String() == "x":
			on := !cur.picked[cur.at]
			if cur.single {
				clear(cur.picked)
			}
			cur.picked[cur.at] = on
			f.recheck()
		}
		return nil, false
	}
	switch msg.Type {
	case tea.KeyLeft:
		cur.cursor = max(cur.cursor-1, 0)
	case tea.KeyRight:
		cur.cursor = min(cur.cursor+1, len(cur.value))
	case tea.KeyHome, tea.KeyCtrlA:
		cur.cursor = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		cur.cursor = len(cur.value)
	case tea.KeyBackspace:
		if cur.cursor > 0 {
			cur.value = append(cur.value[:cur.cursor-1:cur.cursor-1], cur.value[cur.cursor:]...)
			cur.cursor--
		}
	case tea.KeyDelete:
		if cur.cursor < len(cur.value) {
			cur.value = append(cur.value[:cur.cursor:cur.cursor], cur.value[cur.cursor+1:]...)
		}
	case tea.KeyCtrlU:
		cur.value, cur.cursor = cur.value[cur.cursor:], 0
	case tea.KeySpace, tea.KeyRunes:
		// A paste may bring line breaks and tabs, which a field of one
		// line takes as blanks.
		in := []rune(strings.Map(func(r rune) rune {
			if r < ' ' || r == 0x7f {
				return ' '
			}
			return r
		}, string(msg.Runes)))
		if msg.Type == tea.KeySpace {
			in = []rune{' '}
		}
		cur.value = append(cur.value[:cur.cursor:cur.cursor], append(append([]rune(nil), in...), cur.value[cur.cursor:]...)...)
		cur.cursor += len(in)
	default:
		return nil, false
	}
	f.recheck()
	return nil, false
}

// view draws the form, one line per field and one for what is wrong, in
// cols columns.
func (f *dashForm) view(cols int) string {
	var b strings.Builder
	b.WriteString(dashBold.Render(f.title) + "\n")
	width := 0
	for _, fl := range f.fields {
		width = max(width, len(fl.label))
	}
	for i, fl := range f.fields {
		mark := "  "
		if i == f.focus {
			mark = "> "
		}
		line := mark + fl.label + strings.Repeat(" ", width-len(fl.label)) + "  "
		switch {
		case fl.toggle:
			line += box(fl.on) + " " + dashFaint.Render(fl.hint)
		case fl.choices != nil:
			if len(fl.choices) == 0 {
				line += dashFaint.Render(fl.hint)
			}
			line += fl.pickLine(i == f.focus, cols-lipgloss.Width(line))
		case i == f.focus:
			line += string(fl.value[:fl.cursor]) + dashBold.Render("▏") + string(fl.value[fl.cursor:])
			if len(fl.value) == 0 {
				line += dashFaint.Render(fl.hint)
			}
		default:
			line += string(fl.value)
			if len(fl.value) == 0 {
				line += dashFaint.Render(fl.hint)
			}
		}
		b.WriteString(line + "\n")
	}
	if f.note != "" {
		b.WriteString(dashRed.Render("  "+f.note) + "\n")
	}
	b.WriteString(dashFaint.Render("tab next field · ←/→ choose · space picks · enter submit · esc cancel"))
	return b.String()
}

// pickLine is f's choices in room columns: those around the one at, with ‹
// or › where more are hidden to that side, scrolled only as far as at needs.
func (f dashField) pickLine(focused bool, room int) string {
	if len(f.choices) == 0 {
		return ""
	}
	items := make([]string, len(f.choices))
	for j, c := range f.choices {
		items[j] = box(f.picked[j]) + " " + c
	}
	// fits says items first..last, with their markers, take room or less.
	fits := func(first, last int) bool {
		w := 2 * (last - first)
		for _, it := range items[first : last+1] {
			w += lipgloss.Width(it)
		}
		if first > 0 {
			w += 2
		}
		if last < len(items)-1 {
			w += 2
		}
		return w <= room
	}
	first, last := 0, f.at
	for first < f.at && !fits(first, f.at) {
		first++
	}
	for last+1 < len(items) && fits(first, last+1) {
		last++
	}
	var b strings.Builder
	if first > 0 {
		b.WriteString(dashFaint.Render("‹ "))
	}
	for j := first; j <= last; j++ {
		if j > first {
			b.WriteString("  ")
		}
		if focused && j == f.at {
			b.WriteString(dashPicked.Render(items[j]))
		} else {
			b.WriteString(items[j])
		}
	}
	if last < len(items)-1 {
		b.WriteString(dashFaint.Render(" ›"))
	}
	return b.String()
}

func box(on bool) string {
	if on {
		return "[x]"
	}
	return "[ ]"
}
