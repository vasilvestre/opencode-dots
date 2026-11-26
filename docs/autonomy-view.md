# Autonomy view

A read-only page over what `bin/publish-gate` kept from GitHub (ADR 0040).
Nothing on it posts, pushes, releases or deletes, and the dashboard holds no
GitHub token.

- `GET /api/autonomy` lists `STATE_DIR/notes/` (observe, class 3) and
  `STATE_DIR/held/` (propose), newest first, at most 500 files each. Each item
  carries `action`, `ref`, `autonomy`, `class`, `artifact`, `reason`, the time
  from the file name, and `readable`.
- `GET /api/autonomy/artifact?kind=notes|held&name=<file>` serves the artifact a
  gate file points at, as `text/plain`. The caller names the gate file, never a
  path.
- The artifact path must resolve, after symlinks, to a regular file inside
  `STATE_DIR`. A `..` element, a path outside, or a link leaving the tree is
  refused with 404. A path under `/state/` (the container mount) is also tried
  against the dashboard's own `STATE_DIR`. Over 256 KiB answers 413.
- Both routes accept only `GET` with a loopback or `-allow-host` `Host`.
- The page is `ui/views/autonomy.js`: tables "Notes" and "Held", each with a
  link to read the artifact in a dialog.

Releasing a held item is a later workflow and has no control here.
