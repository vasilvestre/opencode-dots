# Emergency stop

One switch for the whole dot, or one per repository. While it is on, nothing is published, no run starts, and a
run in progress is told to stop. Raising it again is a person's act.

```bash
bin/pause all "incident"              # stop everything
bin/pause repo OWNER/REPO "bad merge" # stop one repository
bin/pause resume all                  # or OWNER/REPO
bin/pause status
```

The Home page of the dashboard (owner only) has a **Stop everything** button and a Stop/Resume button on each
repository. A visitor sees none of it, and cannot change it.

A stop is a small file under `STATE_DIR/pause/` (`all.json`, or `OWNER__REPO.json`) with who, why and when. Three
places read it:

| Where | Effect |
| --- | --- |
| `bin/publish-gate` | refuses every action (comment, push, open-pr, merge, file-issue), exit 5, before any autonomy ceiling is read |
| `bin/tend` | skips the repository, or every repository |
| the job queue | offers no job of a stopped repository, refuses a claim (HTTP 423), and cancels a claim already held at its next renewal: the worker then stops its run |

The watcher keeps queuing events while a repository is stopped; they wait, and run after the resume if they have
not expired (24 hours).

What it does not do: kill a run that is not claimed through the queue (a run started by hand, or by the
local `awf-gh`, runs to its end), nor undo what was already published. The gate, which every outward write passes
through, is what makes a stop hold for those.

## Dry run

`autonomy: observe` is the dry run: the dot works, and the gate turns every publication into an internal note
(exit 4) under `STATE_DIR/notes/`, which the dashboard shows. Nothing reaches GitHub. Use it to see what the
dot would do on a repository before it may act.

Tests: `bin/test-pause`, `test/tend.sh`, `cmd/dashboard/pause_test.go`, `internal/jobs`.
