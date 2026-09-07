# Resona Development

- Product direction: a desktop GUI client compatible with ordinary TS3 servers, followed by plugins, bots, and a separately verified compatible server. TUI is not the current target.
- Read `docs/roadmap.md`, `docs/architecture.md`, and relevant ADRs before changing boundaries.
- Keep Go client behavior independent of Wails and frontend types. Centralize frontend bridge access.
- Clearly distinguish offline, local preview, and real network states. Never present simulated messages, voice, or connection success as real TS3 behavior.
- Do not introduce protocol dependencies based only on README claims. Record the evaluated commit and actual integration evidence.
- Keep private identities, credentials, configuration, build artifacts, and local test data out of Git.
- Use `go test ./internal/...` for core changes and `npm run build` in `frontend` for UI changes. Desktop entry checks require built frontend assets. Run native build checks when changing the Wails bridge or application shell.
- Update docs and ADRs when a decision changes. Mark roadmap items complete only with verification evidence.
- Do not create commits, change Git remotes, or push unless the user asks.
