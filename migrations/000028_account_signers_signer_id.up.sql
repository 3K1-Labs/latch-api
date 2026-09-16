-- Backup passkey signers (LATCH_BACKEND_SOLO_BACKUP_SIGNERS.md): a second
-- WebAuthn credential attached to an existing solo smart account as an
-- additional on-chain signer, rather than a second wallet.
--
-- signer_id is the u32 the contract's add_signer returns. It must be
-- recorded at add time — get_context_rule's signers come back as a
-- positionless Vec, and removals make position an unsafe substitute for it.
ALTER TABLE webapp.account_signers ADD COLUMN signer_id INTEGER;

-- Prevents attaching the same credential to the same account twice. Partial
-- because the existing pending-intent rows (credential_id IS NULL) must stay
-- unconstrained on that pair.
CREATE UNIQUE INDEX idx_webapp_account_signers_address_credential
  ON webapp.account_signers(smart_account_address, credential_id)
  WHERE credential_id IS NOT NULL;
