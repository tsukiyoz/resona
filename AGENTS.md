# Resona Development

- Branch names default to `feature/tsukiyo/{module}-{YYYYMMDD}_{task}` or the corresponding `fix/tsukiyo/` prefix. Use the shared owner name `tsukiyo`, never a model name or a `codex/` prefix. Example: `feature/tsukiyo/client-20260907_ts3-session`.
- Agent effort: the main task defaults to `medium`. Choose subagent reasoning effort autonomously by the difficulty of its bounded task: `low` for simple changes, `medium` for ordinary development, and `high` for difficult protocol, concurrency, or architecture analysis. Increase effort when the problem or verification results justify it, rather than defaulting every task to the highest level. Give each subagent a concrete question and relevant evidence; the main agent reviews and integrates its result.

- Product direction: an independent desktop GUI client, server, and Go ecosystem. TS3 is the first compatibility adapter for the client and a future compatible server; a native protocol may follow measured needs and a separate design decision. Plugins and bots build on protocol-independent behavior. TUI is not the current target.
- Desktop performance is a product constraint: target low total memory, background CPU and GPU interference during gaming. Compare release builds under equivalent workloads, including helper processes; do not infer performance from implementation language or package size. Keep decorative effects restrained. Evaluate native GUI alternatives with evidence before replacing Wails; see docs/frontend-evaluation.md.
- Read `docs/roadmap.md`, `docs/architecture.md`, and relevant ADRs before changing boundaries.
- Keep Go client behavior independent of Wails and frontend types. Centralize frontend bridge access.
- Clearly distinguish offline, local preview, and real network states. Never present simulated messages, voice, or connection success as real TS3 behavior.
- For GUI workflow changes, assign a product-experience subagent to review the full user flow: action, waiting, success, failure, cancellation, and exit. Record agreed behavior and acceptance criteria in `docs/product.md`; review implementation against them before delivery. Keep adjacent feature suggestions in the roadmap rather than silently expanding scope.
- Do not introduce protocol dependencies based only on README claims. Record the evaluated commit and actual integration evidence.
- Keep private identities, credentials, configuration, build artifacts, and local test data out of Git.
- Live tests on the user's TS3 server must stay inside a dedicated channel created by Resona for testing. Do not enter existing activity channels, change their settings, or operate other users. If creating the test channel or a required test action needs more permissions, report the specific denial and wait for the user to grant them. Normal login bootstrap is not permission to test in the default channel.
- Use `go test ./internal/...` for core changes and `npm run build` in `frontend` for UI changes. Desktop entry checks require built frontend assets. Run native build checks when changing the Wails bridge or application shell.
- Update docs and ADRs when a decision changes. Mark roadmap items complete only with verification evidence.
- Do not create commits, change Git remotes, or push unless the user asks.
