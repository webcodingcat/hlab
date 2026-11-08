package lab_test

import (
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/lab"
)

func TestNoteShowsFromItsStartUntilItsEndTimesTheTimeScale(t *testing.T) {
	clock := newTestClock()
	l, _ := open(t, lab.Options{TimeScale: 2, Clock: clock})
	key := lab.NoteKey{Kind: "network_member", Owner: 7, Subject: 9}
	l.AddNote(key, lab.Note{Status: "updating", Data: "member"}, 2*time.Second, 4*time.Second)
	l.AddNote(lab.NoteKey{Kind: "network_member", Owner: 8, Subject: 9}, lab.Note{Status: "detaching"}, 0, time.Second)
	for _, step := range []struct {
		advance time.Duration
		shows   bool
	}{
		{0, false}, // it starts after 2 × 2 s
		{3 * time.Second, false},
		{time.Second, true},     // 4 s
		{7 * time.Second, true}, // 11 s
		{time.Second, false},    // 12 s: 2 × (2 + 4) s
	} {
		clock.Advance(step.advance)
		note, shows := l.Notes("network_member", 7)[9]
		if shows != step.shows || shows && (note.Status != "updating" || note.Data != "member") {
			t.Fatalf("at %s: %+v, %v, want shown %v", clock.Now(), note, shows, step.shows)
		}
	}
	if got := l.Notes("network_member", 8); len(got) != 0 {
		t.Errorf("the other owner's note shows after its end: %+v", got)
	}
}

func TestNewerNoteReplacesTheOlder(t *testing.T) {
	clock := newTestClock()
	l, _ := open(t, lab.Options{TimeScale: 1, Clock: clock})
	key := lab.NoteKey{Kind: "network_member", Owner: 7, Subject: 9}
	l.AddNote(key, lab.Note{Status: "updating"}, 0, time.Minute)
	l.AddNote(key, lab.Note{Status: "detaching"}, 0, time.Minute)
	if got := l.Notes("network_member", 7)[9].Status; got != "detaching" {
		t.Errorf("the note says %q, want the newer detaching", got)
	}
}
