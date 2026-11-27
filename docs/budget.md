# Daily budget

ADR 0036 (accepted for this first step; a payer, a monthly view and a per-person cap are not decided). A cap in USD on what the dot may spend in one UTC day on a repository.
Once the repository's runs have cost that much, the dot stops what spends or publishes code on
its own until the next day. It is opt-in: a configuration without the `budget` key is not capped,
reads nothing and generates the same files and the same workflow inputs as before.

```yaml
profiles:
  company:
    autonomy: act
    budget: {daily_usd: 40}        # the company's cap, on the company profile

repos:
  acme/app:
    use: company
    tend: true
    review: true
  acme/lib:
    use: company
    budget: {daily_usd: 12.5}      # a repository may set its own number
```

`daily_usd` is a positive number, at most 10000, with at most two decimals. The key is allowed in
`defaults` (which only supplies the number: a repository still writes `budget: true` to be capped),
in a profile (that is where a company sets it) and on a repository.

## What it does

A cap applies to autonomous actions only. `budget` is refused unless the effective autonomy is
`act` or `auto` (explicit, or derived from `tend.auto_merge`): a repository at `observe` or
`propose` spends nothing autonomous that a cap could bound, because it only prepares and a person
releases. The error says so.

| Where | What a reached cap does |
| --- | --- |
| `bin/tend` | skips the repository for the day, with a message (`skip owner/repo: not tended, daily budget ...`) |
| `fix-issue` | stops before the checkout and before any agent step, so nothing is spent, and leaves a note under `STATE_DIR/notes/`. It asks again just before it publishes: a run of minutes may cross the cap, and then the push and the pull request are a note |
| `review-pr` | the review comment still goes out; a fix push and a merge become a note |
| `tend-repo` | its own issue still goes out (a comment); a push, a pull request and a merge become a note |
| `bin/publish-gate` | with `GATE_BUDGET_OVER=1`, `push`, `open-pr`, `merge` and `file-issue` exit 4 with "daily budget reached" and write a note, at any ceiling and with none. `comment` is unaffected |

The generator emits the cap as the rule input `budget` (`daily_usd=N`) and as a `budget=N` column
in `tend.list`, written only when the repository sets it, like `autonomy=` and `identity=`.
`bin/tend` parses and validates the column and posts the input `budget` only when it is not empty. A
workflow with an empty `budget` input never runs `bin/budget` and is byte for byte what it was.

## Where the spend comes from

The same files the dashboard sums for run cost (ADR 0008): the run index
`STATE_DIR/logs/index.jsonl` and each run's transcript or log, where awf records `cost_usd` per
agent step. `bin/repos spend` reads them (the code is `internal/spend`, which the dashboard shares for
the price sum and for finding the repository of a run). A run counts on the UTC day it started, under
the repository its log names. Nothing else is read, and only repositories and amounts are printed:
never a prompt or a token.

```bash
bin/budget status [OWNER/REPO]     # spend today, and the cap when tend.list has one
bin/budget check OWNER/REPO CAP    # CAP is N or daily_usd=N; exit 0 under, 3 reached (spend >= cap),
                                   # 1 spend cannot be read, 2 bad arguments
bin/repos spend [-day YYYY-MM-DD] [OWNER/REPO]
```

If the spend cannot be read (no `bin/repos`, no `STATE_DIR/logs`, an unreadable index), `check`
fails closed: exit 1, which every caller treats as a reached cap. `BUDGET_FAIL=open` makes it exit 0
with a loud warning instead. A state directory with a `logs` folder and no index yet is no spend, so
a first day is not blocked.

## What it does not do

- It is per repository, per UTC day. No monthly view, no cap for the whole dot.
- It caps autonomous actions only. A person who runs a fix themselves is not stopped.
- No payer tracking: it does not say whose card or quota paid, and there is no per-person cap yet.
- It does not abort a run in progress. Spend lags a run by its own duration, because a run is counted
  from its log, so one run can overshoot the cap. The cap stops the next one.
- A run whose log names no repository is counted under none, so it is in no cap.
- Runs of other dots on the same repository are not seen: only this dot's state is read.

`bin/test-budget` covers the check against fixture logs (under, at, over, unreadable, fail open);
`bin/test-publish-gate` and `bin/test-e2e budget` cover the gate and the workflows.
