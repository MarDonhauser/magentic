# Apply semantic Registry changes across processes

Every Registry mutation is a semantic change applied under interprocess
coordination after rereading the latest durable state, then committed
atomically, so unrelated concurrent mutations are retained and invariants are
enforced once. Writer migration is a roll-forward cutover: new readers accept
legacy state, the first new writer migrates it under the same coordination, and
legacy direct writers must stop before the migrated form is written rather than
participating in dual-write or last-writer-wins behavior.

Where native advisory locking is unavailable, the portable Adapter records an
owner nonce and refreshes a heartbeat. A contender may recover only a lock whose
owner heartbeat is stale, so a legitimate long Registry change is not removed
merely because it lasts longer than a fixed age.
