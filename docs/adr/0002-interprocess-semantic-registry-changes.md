# Apply semantic Registry changes across processes

Every Registry mutation is a semantic change applied under interprocess
coordination after rereading the latest durable state, then committed
atomically, so unrelated concurrent mutations are retained and invariants are
enforced once. Migration is a roll-forward cutover: Registry Snapshot and Change
both normalize legacy state under the same coordination and persist the first
required migration as a new revision. Legacy direct writers must stop before
the migrated form is written rather than participating in dual-write or
last-writer-wins behavior.

Where native advisory locking is unavailable, the portable Adapter records an
owner nonce and refreshes a heartbeat. A contender may recover only a lock whose
owner heartbeat is stale, so a legitimate long Registry change is not removed
merely because it lasts longer than a fixed age.
