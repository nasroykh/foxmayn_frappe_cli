package services

import (
	"context"
	"os"
	"time"
)

// configWatcher polls a file's modification time and size. Polling (not
// file system notifications) keeps it simple and works the same on every
// OS, through the atomic rename config.Edit does, and for a file that does
// not exist yet.
type configWatcher struct {
	path     string
	every    time.Duration
	onChange func(exists bool)
}

type fileStamp struct {
	exists bool
	size   int64
	mod    time.Time
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{exists: true, size: fi.Size(), mod: fi.ModTime()}
}

func (w *configWatcher) run(ctx context.Context) {
	last := stampOf(w.path)
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur := stampOf(w.path)
			if cur != last {
				last = cur
				w.onChange(cur.exists)
			}
		}
	}
}
