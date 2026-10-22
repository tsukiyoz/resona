# ADR-0034: Shared Server Administration and Web Console

Date: 2026-10-22

## Decision

Add an optional single-server HTTP management surface and embedded web console.
Native commands and HTTP use the same authorized business operations; neither
transport writes KV directly. Voice routing stays independent of HTTP, Casbin
evaluation and storage I/O. Authorization is evaluated on control actions.

Casbin v2.135.0 supplies the fixed initial RBAC policy: owner inherits admin;
admin manages channels and ordinary members; only owner delegates administrators,
changes server settings and manages web credentials. No role can moderate owner;
admin cannot moderate another admin. Ownership remains immutable. Identity
assignments join the existing bounded aggregate KV document. The policy is
constructed once and read through Casbin's synchronized enforcer, with no second
policy writer or per-user watcher. Persisted mutations authorize their CAS draft,
including retries. HTTP edits require a baseline revision and reject stale forms.

The console is a server administration surface, not a replacement desktop GUI.
Network/key/storage settings remain deployment configuration. Web authentication
is separate from the voice password and binds to an existing management identity.
Subscriptions share server events and use bounded coalescing snapshots; reconnect
fetches current state rather than unlimited replay. Implementation is delivered as
independent role, credential/API, resource and UI commits. Public policy editing,
multi-server hosting, file transfer and historical audit search are out of scope.

References: [Casbin RBAC](https://casbin.org/docs/rbac/),
[v2.135.0 synchronized enforcer](https://github.com/casbin/casbin/blob/v2.135.0/enforcer_synced.go),
ADR-0033. Server tests exercise the actual model/Enforce/grouping APIs.
