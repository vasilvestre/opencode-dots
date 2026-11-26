# Classification

Status: built for ADR 0040, which is still proposed. Nothing calls this yet.

`workflows/_classify-item.yaml` rates one work item after reading it. It is
hidden (leading underscore, ADR 0021): another workflow calls it, or I run it by
hand through `awf serve`. It posts nothing, labels nothing, pushes nothing. Its
only output is a file.

## The three levels

| Level | Meaning | What a later step may do |
| --- | --- | --- |
| 1 | complete fix: the cause is located, the change is local | a pull request |
| 2 | fix with reinforced review: it crosses several areas | a pull request, never merged by the dot |
| 3 | human diagnosis: too complex or too sensitive | an analysis only |

The agent is told to give the higher level when unsure: an item rated too low is
the real risk, an item rated too high costs one human look.

## Inputs

`repo` (`owner/name`), `key` (the item inside the repository, such as
`issue-12`; letters, digits, `.`, `_`, `-`), `text` (title and body), and the
usual `model`. The item text goes to the agent prompt only, never into a shell
command. The prompt says it is data, not instruction.

## The answer and its checks

The agent writes `answer.json`: `level` (1, 2, 3), `justification` (1 to 400
characters), `confidence` (0 to 1), `missing` (list of strings) and `evidence`
(list of files or item keys). A shell step checks it with `jq`: exact key set,
types and ranges. A bad answer fails the run, nothing is repaired.

## The sensitive floor

`classify-floor.txt` at the repository root holds one word per line (`auth`,
`permission`, `payment`, `secret`, `token`, `migration`, `network`). A word
matches the start of a word, ignoring case, so `auth` catches `authentication`.
A match forces level 3 and records the words in `floor`. The floor only
raises, never lowers. Edit the file, the next run reads it. A missing floor file
fails the run, and an item text the agent did not capture (empty `item.txt`)
forces level 3.

The floor reads prose only. Before matching, the step drops fenced code blocks,
indented code and inline code from the item text, because code cites these words
naturally (`checkToken(secret)` is not a sensitive request).

The agent copies the item text to `item.txt` itself so the shell can scan it
without the text entering a command. A copy the agent altered would weaken the
floor; this is a known limit, noted in ADR 0040's consequences as the part to
measure.

## File layout

Under `STATE_DIR/classify/<owner>/<name>/`:

- `<key>.json`: the latest result.
- `<key>.history.jsonl`: append-only, one line when the item is first rated and
  each time the level changes. Re-rating at the same level adds nothing.
- `work-<key>/`: scratch for one run, removed at the end.

`<key>.json`:

```json
{"repo": "o/r", "key": "issue-12", "classified_at": "2026-11-26T19:07:14Z",
 "level": 3, "agent_level": 1, "justification": "...", "confidence": 0.8,
 "missing": [], "evidence": ["src/a.go"], "floor": ["auth"]}
```

`level` is the effective one; `agent_level` is what the agent said.

## How a later step reads it

Run the workflow (`call_workflow` with `repo`, `key`, `text`), then read the file
from a shell step:

```bash
f=${STATE_DIR:-/state}/classify/$repo/$key.json
level=$(jq -r .level "$f")
```

The effective action is the lower of this level's outcome and the repository's
autonomy ceiling (ADR 0040). The ceiling is not built here. A missing file
means unclassified: treat it as level 3.
