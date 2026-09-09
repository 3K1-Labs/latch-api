-- Retroactively link existing webapp.multisig_members rows to the user who
-- owns the passkey each row is a signer for, so a member already recorded
-- before login-time re-linking (see RelinkMultisigMembersByCredential, called
-- from webauthn_service.FinishAuthentication) shows up in
-- ListMultisigAccountsWithProposalCountForUser without waiting for that
-- member's next sign-in.
--
-- Source of truth is webapp.webauthn_credentials.user_id — after the
-- account-identity change (webauthn_service no longer relocates a credential
-- onto whichever session cookie is present) that column reliably names the
-- credential's owner.
--
-- Only NULL member rows are touched: a row that already carries a user_id was
-- linked at draft-join or register time by a caller who proved that signer,
-- and must not be overwritten by a possibly-staler credentials row.
--
-- credential_id on both tables is the base64url-encoded WebAuthn credential ID
-- (webauthn_service encodes it with base64.RawURLEncoding; the extension sends
-- the same form in register/join payloads).
--
-- Delegated (g_address) members have no webauthn_credentials row and are left
-- untouched — they can only be linked at draft-join / register time.
UPDATE webapp.multisig_members m
SET user_id = wc.user_id
FROM webapp.webauthn_credentials wc
WHERE m.credential_id = wc.credential_id
  AND m.user_id IS NULL;
