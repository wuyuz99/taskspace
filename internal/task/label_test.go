package task

import (
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	long := strings.Repeat("x", labelTitleWidth+10)
	cases := []struct{ name, title, want string }{
		{"PAY-142", "", "PAY-142"},
		{"PAY-142", "  \n ", "PAY-142"},
		{"PAY-142", "Totals endpoint", "PAY-142 - Totals endpoint"},
		{"PAY-142", "two\nlines\tand  tabs", "PAY-142 - two lines and tabs"},
		// BEL and ST would end a terminal's title sequence; ESC starts another.
		{"PAY-142", "a\x07b\x1b]2;evil\u009cc", "PAY-142 - a b ]2;evil c"},
		// A byte that is not UTF-8 becomes U+FFFD, which ends nothing.
		{"PAY-142", "a\x9cb", "PAY-142 - a�b"},
		{"PAY-142", long, "PAY-142 - " + strings.Repeat("x", labelTitleWidth-1) + "…"},
	}
	for _, c := range cases {
		if got := Label(c.name, c.title); got != c.want {
			t.Errorf("Label(%q, %q) = %q, want %q", c.name, c.title, got, c.want)
		}
	}
	tk := New("PAY-142")
	tk.Title = "Totals"
	if got := tk.Label(); got != "PAY-142 - Totals" {
		t.Errorf("Task.Label = %q", got)
	}
}
