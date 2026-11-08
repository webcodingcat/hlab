# The state on disk

How the state is encoded and written. The rules for using it (kinds,
transactions, what copies) are in DEVELOPING.md (The state).

- **Encoding reuses each record's JSON.** `state.Encode` keeps the JSON of every
  record in the state it encoded; `tx.Result()` drops it for the records the Tx
  noted. So a change encodes only its own records, and `Encode` puts the file
  together from the rest. It writes by hand what `json.MarshalIndent` would write
  for the whole file, which `TestEncodeWritesWhatMarshalIndentWrites` checks. This
  relies on the rule that a state's records never change.
- **The file.** The state is `state.json` in the data folder. Its keys are
  `version`, `saved`, `next_ids`, `kinds` and `lab`. Each kind is a list sorted by
  ID, so diffs stay readable. Names are `snake_case`, times are RFC 3339 in UTC.
- **The writer.** `state.Writer` writes the file in the background:
  - It writes only the newest content it was given. Content is written once, even
    if a second wake-up follows; on Windows, replacing a file while someone reads it
    fails with "access denied".
  - It writes `state.json.tmp`, calls fsync and then moves the file over
    `state.json`. On Windows it uses `MoveFileExW` with `MOVEFILE_WRITE_THROUGH`,
    which `os.Rename` does not set. So `state.json` is never a half-written file.
  - When a write fails, it logs the error and tries again by itself: after 1 s, then
    each time twice as long, up to 30 s. A virus scanner or a reader may hold the
    file for a moment. `Flush` reports the error of the last try.
  - After `Lab.Close`, a change (from a request that outlived the stop) is logged
    and dropped.
