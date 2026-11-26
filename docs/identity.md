# Identity: marking what the dot writes

The dot acts under the owner's login, so a pull request by that login is either the
owner's or the dot's, and nothing in GitHub says which. The merge scope "the dot's own
pull requests" (`docs/autonomy.md`) needs a way to tell. ADR 0034 settles the
principle: no default mark, and marking is a choice per repository or profile. This
page is the mechanism.

## The key

`identity` in `repos.yaml`, allowed under a repository, `defaults` and a profile:

| Value | What the dot adds to each body it writes |
| --- | --- |
| `off` (absent) | nothing. The legacy behaviour, byte for byte. |
| `marker` | the invisible marker only. |
| `signed` | a short visible footer naming the dot, then the marker. |

`silent` is not a value: once marking is on, the marker is always kept, because the
merge scope reads it. Under trust `client` and `strict` the minimum is `marker`:
`identity: off`, or no key, is refused by the generator with a message naming the
level (`bin/repos generate`). Elsewhere `off` is the default and stays valid.

The generator hands an explicit value to the rules as the workflow input `identity`.
An absent key emits nothing, so a repository that never set it keeps the file it had. `tend.list` carries an explicit value as an `identity=<mode>` column,
so the daily pass signs too; `bin/tend` posts the input only when the column is there.

## The marker and the footer

`bin/dot-sign WORKFLOW [RUN]` reads a body on stdin and prints it signed, per
`DOT_IDENTITY` (`off`, `marker`, `signed`; empty is `off`):

```
<body>

---
Filed by an automated agent (the dot `NAME`) on behalf of its owner, not written by a person.

<!-- opencode-dots:dot=NAME:WORKFLOW:RUN -->
```

The footer is the first sentence of the one `file-upstream` puts on its upstream
issues; `marker` prints the last line only. `DOT_NAME` is the dot's name (the workflows
pass the login `gh` is signed in as). It never signs a body twice, it is a pure
function of its inputs (no network, no file), and `off` is `cat`. An unknown mode
exits 2 and prints nothing, so a step that pipes through it fails closed.

## Where the workflows sign

Each of `review-pr`, `reply-pr`, `fix-issue`, `tend-repo` and `fix-ci` declares an
optional `identity` input (default empty, pattern `^(off|marker|signed)?$`). Only when
it is non-empty and not `off` does a step pipe a body through `bin/dot-sign`, before the
gate sees it, so a held record keeps the signed text:

| Workflow | Signed |
| --- | --- |
| `review-pr` | the review comment (`post`) |
| `reply-pr` | the reply comment |
| `fix-issue` | the pull request text, the issue comment with the agent's answer |
| `tend-repo` | the issue text, the pull request text |
| `fix-ci` | nothing: it only pushes to a branch, and a commit message is not signed |

`tend-repo` hands its `identity` to the `review-pr` run it starts. With the input empty
or `off`, no step runs `dot-sign` or the extra `gh api user` call, so what reaches GitHub
is what it was; `bin/test-e2e identity` compares calls and posted bodies, and the
`baseline` section still compares the workflows to the ones before the gate.
`file-upstream` already signs its own issues and is unchanged apart from telling the gate
where the finding comes from (below).

## The merge scope

At `auto`, `bin/publish-gate merge` allows a pull request whose author is `DOT_LOGIN`.
With marking on (`GATE_IDENTITY` is `marker` or `signed`) that is no longer enough: the
pull request counts as the dot's own only if its body carries the marker, which the
caller checks and reports as `GATE_PR_BODY_MARKED=1`. Otherwise it is a human's under
the same login and the merge is refused (exit 5) with a reason that says so. `review-pr`
reads the body from the `meta.json` it fetched; `tend-repo` asks `gh pr view`. With
marking off the gate behaves exactly as before. Dependency bots need no marker, and
`GATE_MERGE_SCOPE=all` still widens the scope to every author.

## The held `file-issue` record

`file-upstream` now sets `GATE_REPO_SOURCE` (the repository the finding comes from) and
`GATE_UPSTREAM` (the upstream it is for) when it calls the gate. A held `file-issue`
record gets `repo:` and `upstream:` lines from them, so `publish-held` need not derive
them. Unset, the lines are omitted.

## Tests

`bin/test-dot-sign` (the script), `bin/test-publish-gate` (the merge cells and the
record lines), `internal/repos/features_identity_test.go` (values, defaults, profiles,
the trust minimum) and the `identity` section of `bin/test-e2e` (marked bodies at
`signed`, identical bodies at `off`, a human's pull request refused with marking on, a
marked one merged, a `client` repository refused without a marker).
