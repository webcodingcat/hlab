package logfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// chunkSize is how much the backward reader reads at a time.
const chunkSize = 64 << 10

// levels orders the known log levels; a line without one of them always
// passes a level filter.
var levels = []string{"DEBUG", "INFO", "WARN", "ERROR"}

// Search returns the lines of hlabd.log and its rotated files, newest
// first, whose shown text (level, msg and key=value fields of a JSON line,
// the raw text otherwise) contains query case-insensitively and whose level
// is at least minLevel ("" for all). It stops after max lines; capped
// reports that more lines matched.
func Search(dir, name, query, minLevel string, max int) (lines []string, capped bool, err error) {
	r, err := newBackReader(filepath.Join(dir, name))
	if err != nil {
		return nil, false, err
	}
	defer r.close()
	q := strings.ToLower(query)
	minRank := rank(minLevel)
	for {
		l, ok, err := r.prev()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return lines, false, nil
		}
		level, text := shown(l)
		if minLevel != "" && rank(level) < minRank {
			continue
		}
		if !strings.Contains(strings.ToLower(text), q) {
			continue
		}
		if len(lines) == max {
			return lines, true, nil
		}
		lines = append(lines, string(l))
	}
}

// Before returns the n lines before the newest line equal to line, oldest
// first, across rotated files. atStart reports that no line is left before
// the returned ones; a line not found returns no lines and atStart true.
func Before(dir, name, line string, n int) (lines []string, atStart bool, err error) {
	r, err := newBackReader(filepath.Join(dir, name))
	if err != nil {
		return nil, false, err
	}
	defer r.close()
	for {
		l, ok, err := r.prev()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, true, nil
		}
		if string(l) == line {
			break
		}
	}
	for {
		l, ok, err := r.prev()
		if err != nil {
			return nil, false, err
		}
		if !ok {
			atStart = true
			break
		}
		if len(lines) == n {
			break
		}
		lines = append(lines, string(l))
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines, atStart, nil
}

// rank orders a level as the Logs view does: unknown levels rank highest.
func rank(level string) int {
	for i, l := range levels {
		if strings.EqualFold(l, level) {
			return i
		}
	}
	return len(levels)
}

// shown returns the level of a line and the text the Logs view shows for
// it: level, msg and the key=value fields of a JSON object in their order
// (time excluded), or the raw line when it is not a JSON object.
func shown(line []byte) (level, text string) {
	dec := json.NewDecoder(bytes.NewReader(line))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return "", string(line)
	}
	var msg string
	var fields []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return "", string(line)
		}
		key, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", string(line)
		}
		var s string
		isString := json.Unmarshal(raw, &s) == nil
		switch key {
		case "time":
		case "level":
			if isString {
				level = s
			}
		case "msg":
			if isString {
				msg = s
			} else {
				msg = compact(raw)
			}
		default:
			v := compact(raw)
			if isString && s != "" && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '"' || r == '=' }) {
				v = s
			}
			fields = append(fields, key+"="+v)
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return "", string(line)
	}
	if _, err := dec.Token(); err != io.EOF { // nothing after the object
		return "", string(line)
	}
	return level, level + " " + msg + " " + strings.Join(fields, " ")
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// backReader reads the lines of a log file and then of its rotated files
// backwards, newest first, in chunks. A file being read stays open, so a
// rotation meanwhile does not disturb it; the next older file is found
// from the position of the current one (os.SameFile).
type backReader struct {
	path    string
	f       *os.File
	info    os.FileInfo
	pos     int64  // pending starts at this offset
	pending []byte // unread bytes of the file before the lines returned
	started bool   // the incomplete tail of the file is dropped
	done    bool   // all lines of the file are returned
}

// newBackReader opens the newest existing file of path, path.1 .. path.Keep.
func newBackReader(path string) (*backReader, error) {
	r := &backReader{path: path}
	for i := 0; i <= Keep; i++ {
		f, err := openRead(r.name(i))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := r.use(f); err != nil {
			return nil, err
		}
		return r, nil
	}
	return r, nil
}

func (r *backReader) name(i int) string {
	if i == 0 {
		return r.path
	}
	return rotated(r.path, i)
}

func (r *backReader) use(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.info, r.pos, r.pending, r.started, r.done = f, info, info.Size(), nil, false, false
	return nil
}

func (r *backReader) close() {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
}

// prev returns the line before the last one returned, without its newline;
// ok is false when no line is left.
func (r *backReader) prev() (line []byte, ok bool, err error) {
	for r.f != nil {
		l, ok, err := r.prevInFile()
		if err != nil || ok {
			return l, ok, err
		}
		if err := r.older(); err != nil {
			return nil, false, err
		}
	}
	return nil, false, nil
}

// prevInFile returns the previous line of the current file. An unfinished
// last line is left out.
func (r *backReader) prevInFile() ([]byte, bool, error) {
	for !r.done {
		if i := bytes.LastIndexByte(r.pending, '\n'); i >= 0 {
			if !r.started {
				r.pending, r.started = r.pending[:i], true
				continue
			}
			l := r.pending[i+1:]
			r.pending = r.pending[:i]
			return l, true, nil
		}
		if r.pos == 0 {
			r.done = true
			if r.started {
				return r.pending, true, nil
			}
			return nil, false, nil
		}
		size := min(int64(chunkSize), r.pos)
		chunk := make([]byte, size, size+int64(len(r.pending)))
		if _, err := r.f.ReadAt(chunk, r.pos-size); err != nil {
			return nil, false, err
		}
		r.pos -= size
		r.pending = append(chunk, r.pending...)
	}
	return nil, false, nil
}

// position returns i where the current file is named path.i (0 for path),
// or -1 when it is gone.
func (r *backReader) position() int {
	for i := 0; i <= Keep; i++ {
		if st, err := os.Stat(r.name(i)); err == nil && os.SameFile(r.info, st) {
			return i
		}
	}
	return -1
}

// older moves on to the file rotated before the current one, under the
// name it has now; without one (the end, or rotated away) the reader ends.
func (r *backReader) older() error {
	cur := r.f
	r.f = nil
	defer func() { _ = cur.Close() }()
	for attempt := 0; attempt < 10; attempt++ {
		k := r.position()
		if k < 0 || k >= Keep {
			return nil
		}
		f, err := openRead(r.name(k + 1))
		if err != nil {
			if r.position() != k {
				continue // rotated meanwhile
			}
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
				return nil // no older file, or one being deleted
			}
			return err
		}
		if r.position() != k {
			_ = f.Close() // rotated meanwhile: the file may not be the next one
			continue
		}
		return r.use(f)
	}
	return nil
}
