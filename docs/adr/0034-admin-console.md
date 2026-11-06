# ADR-0034: Shared Server Administration and Web Console

Date: 2026-10-22

## Decision

Add an optional single-server HTTP management surface and embedded web console.
Native commands and HTTP use the same authorized business operations; neither
transport writes KV directly. Voice routing stays independent of HTTP, Casbin
evaluation and storage I/O. Authorization is evaluated on control actions.

Casbin v2.135.0 supplies the fixed initial RBAC policy: owner inherits admin;
admin manages channels and ordinary members; only owner delegates administrators,
changes server settings and manages bans. No role can moderate owner;
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

## 2026-11-06: Direct Roles and Channel Admission

Remove reusable management bearer credentials, administrator invitations, their
native redemption command, UI and issuance CLI. Owner stays unique and claim-only.
Existing account UIDs and roles remain; retired credential/invitation fields are
discarded on decode and omitted from the next successful KV write. Native reply
code 14 stays reserved. Automatic rotating account recovery tokens remain internal
login credentials and do not grant roles.

The Owner-only Administrators page directly grants/revokes admin on registered
accounts. Removing admin invalidates management sessions and capabilities while
retaining the account and voice connection. Browser login accepts account/password
only and does not rotate native recovery credentials or replace voice connections.

Channel access is an `admin_only` boolean. The default is always ordinary.
An ordinary member/guest cannot actively join a restricted channel; an online
admin/owner already in that destination may move them in temporarily. The grant
belongs only to that peer and channel, ends on departure/disconnect/UID change,
and is never restored or persisted. Inviter departure does not end an existing
admission. Native and web moderation share final role, instance and presence
checks. Native moderation carries member ID plus connection instance; web writes
also retain their baseline revision.

Channel tightening and role revocation recheck current occupants, move ineligible
peers to the default, advance voice epoch/chat generation, and preserve device,
mute and listen intent. No authorization/storage work is added per voice frame.
Admin may move/kick ordinary members/guests; Owner may also moderate admins.
Only Owner may ban/unban non-Owner identities. Kicks stop automatic reconnect but
retain account recovery credentials for a later explicit connection.
