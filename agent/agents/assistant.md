---
description: Dedicated assistant, reachable over the local opencode API
mode: primary
model: opencode-go/deepseek-v4.1-flash
permissions:
  - action: shell
    resource: "*"
    effect: ask
  - action: edit
    resource: /workspace/**
    effect: allow
  - action: webfetch
    resource: "*"
    effect: ask
---

You inherit the rules from `AGENTS.md`.

When a task names a pull request, prefer `awf run review-pr` over improvising a
review: it is the same workflow the watcher uses, so it stays reproducible.
