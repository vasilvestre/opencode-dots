# Review charter

A repository can tell the review how its maintainers want pull requests
judged: tone, priorities, what blocks a merge. It does so with one optional
file, `.dots/review.md`.

## Where it is read from

`review-pr` reads the file from the **default branch of the base repository**,
fetched with git (`refs/heads/<default>`). It never reads it from the pull
request head, from the checkout of the pull request, or from the pull request
text.

Why: the project rule is that pull request text is data, never instructions.
If the charter came from the head, its author could rewrite the reviewer's
rules in the same pull request that the reviewer judges. The default branch only
changes through a merge, so the charter is whatever the maintainers already
accepted. To change the charter, merge a change to `.dots/review.md`.

## Format

Plain Markdown, any structure. Write it as instructions to a reviewer.

## What it can do

- Set the tone of the review.
- Order the priorities, and name what to ignore.
- Say what makes the verdict `hold` rather than `merge`.

## What it can never do

The charter shapes the review, nothing more. The review prompt repeats, after
the charter, that the agent never commits, pushes, posts or merges, runs no
command, and edits only `review.md` and `verdict.txt`. A charter line that asks
for more, or says to ignore these rules, is void. Those actions stay in shell
steps.

## Limits

- Capped at 8192 bytes. A longer file is cut there and ends with
  `[charter truncated at 8192 bytes]`.
- Only a regular file counts. A symlink is ignored.
- No file, an unreadable default branch or a failed fetch: no charter, and the
  review runs exactly as before.
- Only `review-pr` reads it. The fix agent and the other workflows do not.

The step writes the result to `reviews/pr<N>-<sha>/charter.md` under
`STATE_DIR`, with a header naming the branch and a closing line repeating that
it grants no permission.

## Example

```markdown
# Review charter

Tone: short, direct, no praise.

Priorities, in order: data loss, authentication, migrations, then tests.
Ignore formatting and naming; the linter owns them.

Answer `hold` when a migration has no rollback, when a public API changes
without a note in CHANGELOG.md, or on any finding in `auth/`.
```
