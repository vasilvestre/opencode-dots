# Autonomy view

A page over what `bin/publish-gate` kept from GitHub (ADR 0040). It reads
files, and its one action files an approval: the Release button starts the
`publish-held` workflow through awf, which publishes. The dashboard holds no
GitHub token and never posts, pushes or deletes itself.

- `GET /api/autonomy` lists `STATE_DIR/notes/` (observe, class 3) and
  `STATE_DIR/held/` (propose), newest first, at most 500 files each. Each item
  carries `action`, `ref`, `autonomy`, `class`, `artifact`, `reason`, the time
  from the file name, and `readable`, plus `target` and `title` when the record
  has them (a `file-issue` record).
- `GET /api/autonomy/artifact?kind=notes|held|released&name=<file>` serves the artifact a
  gate file points at, as `text/plain`. The caller names the gate file, never a
  path.
- The artifact path must resolve, after symlinks, to a regular file inside
  `STATE_DIR`. A `..` element, a path outside, or a link leaving the tree is
  refused with 404. A path under `/state/` (the container mount) is also tried
  against the dashboard's own `STATE_DIR`. Over 256 KiB answers 413.
- Both routes accept only `GET` with a loopback or `-allow-host` `Host`.
- `GET /api/autonomy` also returns `released`: the records `publish-held` moved
  to `STATE_DIR/held/released/`.
- `POST /api/autonomy/release` with `{"id": "<held file name>"}` starts
  `publish-held` with that `id`, the way the Ask page starts `ask-pr` (a post to
  `awf serve`, never `awf run`). It accepts only `POST` with a loopback or
  `-allow-host` `Host`, a JSON body, and a record that exists under `held/` and
  is not under `held/released/` (409 when it is, 404 when absent, 502 when awf
  refuses). For a `file-issue` record it also sends `repo` and `upstream`
  (from the record's `source:` line and `awf-gh.yaml`; 422 when it cannot tell),
  every other action sends `id` only. It returns `{execution_id}`. It does not wait for the run, and the
  record stays in `held/` until the workflow moves it.
- `publish-held` moves a released record's artifact copy to
  `held/released/artifacts/<record>/` and removes such copies after 30 days. The
  record is not rewritten: for `kind=released` the endpoint looks under
  `held/artifacts/` first and then under `held/released/artifacts/`.
- awf's run request carries inputs only, so the dashboard cannot set
  `RELEASED_BY`; the workflow names the account behind its token in the log.
- The page is `ui/views/autonomy.js`: tables "Notes", "Held" and "Released". A
  Held row with a readable artifact has a Release button; a confirm dialog shows
  the action, ref and artifact first. Reload to see the move to "Released".

# Health view

`GET /api/health` returns `STATE_DIR/health.json` (written by `bin/health
--json` on a timer, `docs/health.md`) as `{present, overall, checks, mtime}`,
`GET` only, same host rule. `ui/views/health.js` shows the overall status and
each check (ok, warn, fail) with the age of the file. A missing, unparsable or
over 30 minute old file shows "No health data yet". Nothing runs a check.
