package task

import (
	"errors"
	"fmt"
)

// Task records carry the version of their format, so a tspace can tell what it
// is reading.
//
// The policy:
//
//   - Load reads any version from 1 to CurrentVersion as it is, and rewrites
//     nothing: listing, status and showing a task never change a record. A
//     newer version is refused with ErrUnsupportedVersion and left alone, as
//     is a record with no version, whose notes are inline: this tspace would
//     drop them, and v0.3 moves them into files.
//   - Save writes a record in the oldest format that holds everything in it
//     (see formatVersion): a change that only some records use, such as saved
//     windows, locks an older tspace out of those records alone. Every write
//     goes through Save under the task's lock; see Lock.
//   - An older tspace may not know a field and would drop it on saving, so a
//     new field that matters gets a new version here, which it refuses. Going
//     back past a format change means losing access to records written since,
//     never silently damaging them.
//
// docs/FORMATS.md says what is promised to readers outside tspace, and
// TestRecordFieldsAreDecided stops a new field from skipping this policy.
//
// config.json, projects and signals carry no version: they change only by
// adding optional fields, which older and newer tspace alike ignore or leave
// empty. They will get one when a change cannot be made that way.
const CurrentVersion = 4

// ErrUnsupportedVersion is returned for a record from a newer tspace.
var ErrUnsupportedVersion = errors.New("record written by a newer tspace")

// ErrObsoleteRecord is returned for a record from before versions.
var ErrObsoleteRecord = errors.New("record too old for this tspace")

// formatVersion is the oldest format that holds everything in t. Version 2
// adds only saved windows, version 3 only assumed bases and version 4 only
// reference repositories, so a task without them stays readable by an older
// tspace. An older one would take a reference for a repository to work in,
// with no branch.
func (t *Task) formatVersion() int {
	for _, r := range t.Repos {
		if r.Reference {
			return 4
		}
	}
	for _, r := range t.Repos {
		if r.BaseAssumed {
			return 3
		}
	}
	if len(t.Windows) > 0 {
		return 2
	}
	return 1
}

// checkVersion refuses a record that cannot be read as it stands.
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

// checkStored is checkVersion for a record read from disk.
func checkStored(t *Task) error {
	if t.Version == 0 {
		return fmt.Errorf("task %q: %w; to keep its notes, change it once with tspace v0.3, such as with tspace task update %s --title TITLE, then use this tspace", t.Name, ErrObsoleteRecord, t.Name)
	}
	return checkVersion(t)
}

// stamp sets the version t is saved with.
func stamp(t *Task) error {
	if err := checkVersion(t); err != nil {
		return err
	}
	t.Version = t.formatVersion()
	return nil
}
