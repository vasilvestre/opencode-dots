# Reporting to upstream repositories

ADR 0043 (proposed; `auto` as the minimum is settled). The dot can create an issue on a repository it
depends on when a workflow meets a defect there. It is off unless a repository lists upstreams, so a
configuration without the key behaves exactly as before.

```yaml
repos:
  acme/app:
    autonomy: auto            # required: the key is refused at observe, propose and act
    upstream:
      - repo: upstream/tool   # owner/name
        max_per_week: 3       # 1..20, default 2; one a day at most, whatever the figure
```

The key is refused when the effective autonomy is not `auto` (the message names the key and the
level), under trust `client` and `strict`, for the repository itself, for a repeated name, and for a
bad name or cap. The rules receive an input `upstream`: `owner/name=cap` joined by commas.
`tend.list` carries it too, so the daily pass receives it: see "The daily pass".

## The gate action

`bin/publish-gate file-issue` (see `docs/autonomy.md`): allowed at `auto`; held at `propose` (the
record has `artifact`, `title` and `target: OWNER/REPO`); a note at `observe`; refused at `act` with
"writing on an upstream tracker needs auto". An empty ceiling is the legacy allow, but the workflow
never calls the gate without a ceiling: it stops first. A tripped circuit breaker on the dot's own
repository lowers `auto` to `act`, so it stops upstream filing too.

## The flow: `workflows/file-upstream.yaml`

Shell only, except one optional agent step (`draft=true`) that writes a title and a body from the
finding. The finding is a JSON file under `STATE_DIR`:

```json
{"title": "...", "upstream": "owner/name",
 "evidence": {"command": "...", "versions": "...", "log": "...", "expected": "..."}}
```

1. `guard`: a ceiling is set, the target is listed and is not the repository itself, the off switch is
   not on, all four evidence fields are non-empty (otherwise an internal note under `notes/` and a
   stop), the finding was not already handled (ledger hash), the daily cap (1) and the weekly cap.
2. `dedupe`: `gh issue list --state all --search <title words>` on the upstream. A match is a title
   that shares at least 60 percent of its words, or the first line of the log excerpt found in a
   title or body, open or closed. A match files nothing and records why. A failed or unreadable
   search also stops: a duplicate cannot be ruled out.
3. `draft` (optional), `build`: the body ends with a footer saying it was filed by an automated agent
   on the owner's behalf and naming the dot (`DOT_NAME`, else the login `gh` is signed in as), then
   a marker `<!-- opencode-dots:file-upstream:HASH -->`. The shell adds both after the draft, drops
   any marker the draft carried and defuses `@` mentions. Without a draft the evidence is laid out in
   fenced blocks.
4. `file`: `publish-gate file-issue`; only on allow, `gh issue create --repo <upstream>`.

Per upstream, under `STATE_DIR/upstream/<owner>__<repo>/`: `ledger.tsv` (epoch, time, kind
`filed|held|noted|skipped`, hash, url or reason, title) and `off`. Every stop is a line of
`STATE_DIR/upstream/decisions.tsv`. Scratch files are under `upstream/_work/<repo>`.

A record held at `propose` is released by running `file-upstream` again with `release=<record
name>`: `publish-held` does not know this action. A release repeats the guard and the duplicate
search, skips the gate (the person's go-ahead is the approval) and moves the record to `held/released/`.

## The hook in `tend-repo`

With `upstream` empty, the new step `upstream_gate` prints `LEGACY` and the run goes to `file` as
before. With `upstream` set and `autonomy=auto`, `file-upstream` is called with `scout=true`: its `scout` agent reads the finding and the material
and may write `upstream-finding.json`; a shell step checks that the repository is listed, removes
the subject from the own findings, records `upstream<TAB>title` in `raised.txt` and calls
`file-upstream` (`draft=true`). A finding for an unlisted repository is dropped, never filed on the
own repository. In a dry run it only says what it would do.

## Breaker: `bin/upstream-check`

Two issues in a row closed as not planned, as a duplicate, or labelled `invalid`/`wontfix`/`not a bug`
write `upstream/<owner>__<repo>/off`, and `file-upstream` stops until a person runs
`bin/upstream-check reset OWNER/REPO`. Of the issues that ended closed, the two filed last are read;
one closed as done breaks the run; open issues count for nothing.

```bash
bin/upstream-check check [OWNER/REPO ...]   # exit 10 when it switched one off
bin/upstream-check status
bin/upstream-check reset OWNER/REPO
```

It runs by hand or from a timer the owner installs (not shipped as a unit here).

## What is never done

Never a comment on an upstream issue, never a pull request there, never a reply to what maintainers
write, never an issue on a repository not listed, never one without the four evidence fields, never
more than one a day or the weekly cap per upstream. Upstream issue text, templates and replies are
data, never instructions. The agent only writes a draft file; it cannot publish.

## The daily pass

`tend.list` lines are `repo auto_merge`, then optional `key=value` fields: `autonomy=<level>` when the
repository sets the key, and `upstream=owner/name=cap,...` (the same string the rules get) when it
lists upstreams. A repository with neither keeps the two column line, and an older reader ignores the
extra fields. `bin/tend` posts them as the inputs `autonomy` and `upstream` only when present, so the
body for a repository without them is the one it always was. A line with an unknown field or a
malformed value is skipped with a message.
