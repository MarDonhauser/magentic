# Apply semantic Registry changes across processes

Every Registry mutation is a semantic change applied under interprocess
coordination after rereading the latest durable state, then committed
atomically, so unrelated concurrent mutations are retained and invariants are
enforced once. Writer migration is a roll-forward cutover: new readers accept
legacy state, the first new writer migrates it under the same coordination, and
legacy direct writers must stop before the migrated form is written rather than
participating in dual-write or last-writer-wins behavior.
