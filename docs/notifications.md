# Discord notifications



`review-pr` and `reply-pr` end with a `notify` step. When `DISCORD_WEBHOOK` is
set in `.env`, it posts one line plus the PR link to that channel webhook. The
`reply-pr` step stays silent when the agent decided no answer was needed. The
step never fails the workflow: an unset variable or a dead webhook only prints
a line in the logs.

Create the webhook in the target channel: channel settings, Integrations,
Webhooks, New Webhook, Copy Webhook URL.

The dashboard sends a separate notice when any recorded run finishes with
`workflow.completed` or `workflow.failed`. It posts the workflow name and that
state once per execution. Open the Config view to set the webhook and enable
these notices. The page saves the setting in `notifications.json` at the
repository root, which is private and gitignored. Start from
`notifications.example.json`:

```bash
cp notifications.example.json notifications.json
```

Set `webhook` to a Discord channel URL of the form
`https://discord.com/api/webhooks/<id>/<token>` and set `enabled` to `true`,
either in the file or on the Settings page. A save takes effect on the next
finished run without a restart. A dead webhook or a slow network only writes a
line to the dashboard log. This setting is separate from `DISCORD_WEBHOOK`: the
workflow `notify` steps keep using the environment variable. There is no test
button; saving the config does not send a message.
