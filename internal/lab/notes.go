package lab

import (
	"time"
)

// NoteKey names what a note is about: a kind ("network_member"), the
// resource that shows it (a network) and the one it is about (a server).
type NoteKey struct {
	Kind    string
	Owner   int64
	Subject int64
}

// Note is a status that the API shows for a while after a change, as
// Hetzner shows some for seconds after the change that caused them: a
// server that left a network stays among its members as "detaching", and
// the others show "updating". Data holds what the API needs to show it,
// such as the member that left. Notes live in memory only: after a
// restart their seconds are long over.
type Note struct {
	Status string
	Data   any
	from   time.Time
	until  time.Time
}

// AddNote shows a note from after on for lasting, both Hetzner's times,
// which the time scale scales as it does Sleep's; a note of the same key
// replaces the older one. It takes its own lock, not the lab's mutex, so
// that plans and views, which hold the mutex, may call it.
func (l *Lab) AddNote(key NoteKey, note Note, after, lasting time.Duration) {
	now := l.Now()
	note.from = now.Add(l.scaled(after))
	note.until = note.from.Add(l.scaled(lasting))
	l.notesMu.Lock()
	defer l.notesMu.Unlock()
	if l.notes == nil {
		l.notes = map[NoteKey]Note{}
	}
	l.notes[key] = note
}

// Notes returns the notes of a kind and owner that show now, by subject,
// and forgets those whose time is over.
func (l *Lab) Notes(kind string, owner int64) map[int64]Note {
	now := l.Now()
	l.notesMu.Lock()
	defer l.notesMu.Unlock()
	showing := map[int64]Note{}
	for key, note := range l.notes {
		switch {
		case !now.Before(note.until):
			delete(l.notes, key)
		case key.Kind == kind && key.Owner == owner && !now.Before(note.from):
			showing[key.Subject] = note
		}
	}
	return showing
}

// scaled returns one of Hetzner's times scaled by the time scale.
func (l *Lab) scaled(hetzner time.Duration) time.Duration {
	return time.Duration(float64(hetzner) * l.timeScale)
}
