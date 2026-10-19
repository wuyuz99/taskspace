package task

import (
	"errors"
	"fmt"
)

// Task records carry the version of their format, so a tspace can tell what it
// is reading and bring old records up to date.
//
// The policy:
//
//   - A record without a version is version 0, the format before versions:
//     its brief and handoff may still be inline.
//   - Load reads any version up to CurrentVersion as it is, and rewrites
//     nothing: listing, status and showing a task never change state. A
//     newer version is refused with ErrUnsupportedVersion and left alone.
//   - Save brings the record to CurrentVersion first, running each migration
//     in order. Every write goes through Save under the task's lock (see
//     Lock), so a migration is a write like any other: it starts from the
//     latest record and cannot race an update. Update upgrades before it
//     applies its change, and so must anything that writes a note of an
//     outdated record: a note cleared before the move would be refilled by it.
//   - A migration finishes everything it does outside the record, such as
//     writing note files, before the record is replaced, and Save replaces it
//     only after every migration succeeded. A failure leaves the record as it
//     was, and the next write simply tries again; a migration must therefore
//     be safe to repeat after partial work.
//   - An older tspace ignores the version, as it ignores any field it does not
//     know, and may write the record back without it. That is harmless while
//     every migration is safe to repeat. A newer one refuses to touch what it
//     cannot read, so downgrading past a format change means losing access to
//     records written since, never silently damaging them.
//
// config.json, projects and signals carry no version: they change only by
// adding optional fields, which older and newer tspace alike ignore or leave
// empty. They will get one when a change cannot be made that way.
const CurrentVersion = 1

// ErrUnsupportedVersion is returned for a record from a newer tspace.
var ErrUnsupportedVersion = errors.New("record written by a newer tspace")

// migrations[v] brings a record from version v to v+1.
var migrations = []func(s *Store, t *Task) error{
	// 0 -> 1: move the inline brief and handoff into note files.
	(*Store).moveLegacyNotes,
}

func init() {
	if len(migrations) != CurrentVersion {
		panic("task: one migration per version is required")
	}
}

// Outdated reports whether t's record is in an older format, which its next
// save brings up to date.
func (t *Task) Outdated() bool { return t.Version < CurrentVersion }

func checkVersion(t *Task) error {
	if t.Version < 0 {
		return fmt.Errorf("task %q: format version %d is not one tspace ever wrote; the record is damaged", t.Name, t.Version)
	}
	if t.Version > CurrentVersion {
		return fmt.Errorf("task %q: %w (format version %d; this tspace reads up to %d); upgrade tspace to use it",
			t.Name, ErrUnsupportedVersion, t.Version, CurrentVersion)
	}
	return nil
}

// upgrade runs the migrations from t's version to CurrentVersion, advancing
// the version only past each one that succeeded.
func (s *Store) upgrade(t *Task) error {
	if err := checkVersion(t); err != nil {
		return err
	}
	for t.Version < CurrentVersion {
		if err := migrations[t.Version](s, t); err != nil {
			return fmt.Errorf("upgrade task %q from format version %d: %w", t.Name, t.Version, err)
		}
		t.Version++
	}
	return nil
}
