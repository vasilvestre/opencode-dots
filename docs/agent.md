# The agent configuration



`agent/` is mounted as the container's opencode config root, so the bot's
instructions, agents and skills are versioned here and never mixed with your
personal `~/.config/opencode`.

- `agent/AGENTS.md`: the standing rules.
- `agent/agents/assistant.md`: the agent, its model and its permissions.

This mount applies to `opencode=docker`. With `opencode=local`, the agent uses
your own `~/.config/opencode` and its default agent instead.
