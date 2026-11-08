package cli

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// full says the form takes the whole screen: it has a list to show.
func (f *dashForm) full() bool {
	for _, fl := range f.fields {
		if fl.choices != nil {
			return true
		}
	}
	return false
}

func (f *dashForm) hasToggle() bool {
	for _, fl := range f.fields {
		if fl.toggle {
			return true
		}
	}
	return false
}

// shown are the indexes of the choices the filter keeps: those with what
// was typed in them, in any case.
func (f dashField) shown() []int {
	want := strings.ToLower(string(f.filter))
	var out []int
	for i, c := range f.choices {
		if strings.Contains(strings.ToLower(c), want) {
			out = append(out, i)
		}
	}
	return out
}

// pickKey handles a key in a pick with choices. Letters go to the filter, so
// space is the only key that picks.
func (f *dashField) pickKey(msg tea.KeyMsg) {
	shown := f.shown()
	pos := slices.Index(shown, f.at)
	switch msg.Type {
	case tea.KeyUp, tea.KeyLeft:
		if pos > 0 {
			f.at = shown[pos-1]
		}
	case tea.KeyDown, tea.KeyRight:
		if pos >= 0 && pos < len(shown)-1 {
			f.at = shown[pos+1]
		}
	case tea.KeySpace:
		if pos >= 0 {
			on := !f.picked[f.at]
			if f.single {
				clear(f.picked)
			}
			f.picked[f.at] = on
		}
	case tea.KeyBackspace:
		if len(f.filter) > 0 {
			f.setFilter(f.filter[:len(f.filter)-1])
		}
	case tea.KeyCtrlU:
		f.setFilter(nil)
	case tea.KeyRunes:
		if string(msg.Runes) == " " {
			f.pickKey(tea.KeyMsg{Type: tea.KeySpace})
			return
		}
		// A name has no blanks or control characters, so a filter keeps none.
		in := strings.Map(func(r rune) rune {
			if r <= ' ' || r == 0x7f {
				return -1
			}
			return r
		}, string(msg.Runes))
		f.setFilter(append(f.filter, []rune(in)...))
	}
}

// setFilter filters the list by filter, keeping the cursor on its choice if
// that is still shown, else moving it to the first that is.
func (f *dashField) setFilter(filter []rune) {
	f.filter = filter
	if shown := f.shown(); len(shown) > 0 && !slices.Contains(shown, f.at) {
		f.at = shown[0]
	}
}

// pickSummary is the line beside a pick's label: what is picked, and, when
// it is focused, the filter and which part of the list room lines show.
func (f dashField) pickSummary(focused bool, room int) string {
	var picked []string
	for i, c := range f.choices {
		if f.picked[i] {
			picked = append(picked, c)
		}
	}
	parts := []string{dashFaint.Render("none picked")}
	if len(picked) > 0 {
		parts = []string{strings.Join(picked, ", ")}
	}
	if !focused {
		return parts[0]
	}
	shown := f.shown()
	if len(f.filter) > 0 {
		parts = append(parts, "filter "+string(f.filter)+dashBold.Render("▏")+
			dashFaint.Render(fmt.Sprintf(" %d of %d", len(shown), len(f.choices))))
	} else {
		parts = append(parts, dashFaint.Render("type to filter"))
	}
	if first, last := listWindow(len(shown), slices.Index(shown, f.at), room); last-first < len(shown) {
		parts = append(parts, dashFaint.Render(fmt.Sprintf("%d-%d of %d", first+1, last, len(shown))))
	}
	return strings.Join(parts, dashFaint.Render(" · "))
}

// pickList is a focused pick's list, a line for each choice the filter
// shows, at most room of them, around the one at the cursor.
func (f dashField) pickList(indent string, room int) []string {
	shown := f.shown()
	if len(shown) == 0 {
		return []string{indent + dashFaint.Render("nothing matches; backspace or ctrl+u clears the filter")}
	}
	first, last := listWindow(len(shown), slices.Index(shown, f.at), room)
	var out []string
	for _, i := range shown[first:last] {
		item := box(f.picked[i]) + " " + f.choices[i]
		if i == f.at {
			item = dashPicked.Render(item)
		}
		out = append(out, indent+item)
	}
	return out
}

// listWindow is the part of a list of n, from first up to last, that room
// lines show with pos in it, as near the middle as the ends allow.
func listWindow(n, pos, room int) (first, last int) {
	if n <= room {
		return 0, n
	}
	first = min(max(pos-room/2, 0), n-room)
	return first, first + room
}
