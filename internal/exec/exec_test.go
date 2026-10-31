package exec

import (
	"context"
	"strings"
	"testing"
)

func TestWithEnvKeepsPWDOnDir(t *testing.T) {
	dir := t.TempDir()
	ctx := WithEnv(context.Background(), []string{"PATH=/usr/bin:/bin", "PWD=/stale"})
	r, err := Real().Run(ctx, dir, "env")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Stdout, "PWD="+dir+"\n") {
		t.Fatalf("env = %q, want PWD=%s", r.Stdout, dir)
	}
}
