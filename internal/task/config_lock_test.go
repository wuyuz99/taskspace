package task

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestLockConfigKeepsConcurrentChanges(t *testing.T) {
	s := NewStore(t.TempDir())
	const writers = 16
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := s.LockConfig(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer unlock()
			c, err := s.LoadConfig()
			if err != nil {
				t.Error(err)
				return
			}
			if c.Repos == nil {
				c.Repos = map[string]string{}
			}
			c.Repos[fmt.Sprintf("r%d", i)] = "/src"
			if err := s.SaveConfig(c); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	c, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repos) != writers {
		t.Fatalf("kept %d of %d changes", len(c.Repos), writers)
	}
}

func TestLockConfigGivesUpWithItsContext(t *testing.T) {
	s := NewStore(t.TempDir())
	unlock, err := s.LockConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.LockConfig(ctx); err == nil {
		t.Fatal("took a lock that is held")
	}
}
