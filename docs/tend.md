# Tend: learn from what shipped



`reply-pr` and `review-pr` react. `tend-repo` looks back. Once a day,
`bin/tend` runs it for every repository with `tend` enabled in `repos.yaml`
(it reads the generated `tend.list`):

```bash
$EDITOR repos.yaml       # tend: true, or tend: {auto_merge: true}
make repos
bin/tend                 # or: systemctl --user start dots-tend.service
```

One pass reads what happened since the last one (my commits with their diffs,
the comments I received, the issues already filed) and writes three things:

- `state/tend/<repo>/lessons.md`: durable rules the material proves. `reply-pr`
  and `review-pr` read this file before acting, so a mistake made once becomes
  a rule for every later run in that repository.
- At most one issue, and only when the material proves the defect: a red check,
  a revert, a fix commit that followed mine, a maintainer or CI comment, or a
  reported wrong output. `state/tend/<repo>/raised.txt` records every subject
  a pass filed or skipped, so it does not come back under another wording.
- At most one pull request, only for that finding, only when the fix is
  mechanical and under twenty lines, and none while an earlier `tend/` pull
  request is still open. `auto_merge=true` asks GitHub to merge it once the
  checks pass; leave it false for repositories you do not own.

The timer is not enabled by the install, because each pass spends one agent run
per repository:

```bash
systemctl --user enable --now dots-tend.timer
```
