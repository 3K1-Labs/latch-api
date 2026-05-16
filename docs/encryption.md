# Backup Encryption

Credential backups are encrypted server-side before being stored. The mobile client sends plaintext over TLS — the backend performs all encryption and holds the keys. The encryption strategy is versioned via the `encryption_version` column on `credential_backups`, which allows both phases to coexist during migration.

---

## Phase 1 — DB-stored key (current default)

`encryption_version = 1`

When a backup is first stored for a user, a random 256-bit AES key is generated and written to the `user_encryption_keys` table. All subsequent reads and writes for that user use this stored key.

**Weakness:** The key and the ciphertext live in the same database. A full database dump gives an attacker everything they need to decrypt all backups. Phase 1 exists only as a bootstrap — it should not be the long-term production strategy.

**Relevant code:**
- `internal/service/encryption_service.go` — `KeyForUser`, `EncryptBackup`, `DecryptBackup`
- `internal/service/encryption.go` — AES-256-GCM primitives (`Encrypt`, `Decrypt`, `GenerateKey`)
- `migrations/` — `user_encryption_keys` table

---

## Phase 2 — PBKDF2-derived key (target production state)

`encryption_version = 2`

The encryption key is never stored. It is derived on demand from three inputs:

```
key = PBKDF2(
  password: user_email,
  salt:     user_uuid (bytes),
  secret:   SERVER_PEPPER,
)
```

`SERVER_PEPPER` is a server-side secret that lives only in the environment or secrets manager — never in the database. A full database dump is useless to an attacker without it.

**Activated by:** setting `SERVER_PEPPER` to a non-empty value in the environment.

**Generate a pepper:**
```bash
openssl rand -hex 32
```

**Relevant code:**
- `internal/service/encryption_service.go` — `DecryptBackup` case `EncVersionPBKDF2`
- `internal/service/encryption.go` — `DeriveKeyPBKDF2`

---

## Migration: Phase 1 → Phase 2

Migrating existing users is a re-encryption job. The `DecryptBackup` switch handles both versions, so old and new rows coexist safely throughout.

**Steps:**

1. Set `SERVER_PEPPER` in the production environment and deploy.

2. For each user with `encryption_version = 1`:
   - Fetch their `credential_backups` row.
   - Decrypt using `EncryptionService.DecryptBackup` (will use the Phase 1 DB key).
   - Re-encrypt using `Encrypt` with the PBKDF2-derived key (`DeriveKeyPBKDF2(email, pepper, uuidBytes)`).
   - Update the row: set new `encrypted_blob`, `iv`, `auth_tag`, and `encryption_version = 2`.
   - Delete the user's row from `user_encryption_keys`.

3. Once all rows show `encryption_version = 2`, the `user_encryption_keys` table is empty and can be dropped in a follow-up migration.

> **Important:** `SERVER_PEPPER` is permanent once set in production. Changing it after any backups are encrypted under Phase 2 will make those backups unrecoverable. Treat it like a database master key — store it in a secrets manager and back it up securely.

---

## ENCRYPTION_MASTER_KEY

This environment variable is loaded by `internal/config/config.go` (`requireEnv`) but is **not passed to or used by any service**. It was likely intended for a key-encryption-key (KEK) pattern — wrapping per-user keys with a master key before storing them in the DB — but was not implemented.

It can safely be downgraded from `requireEnv` to `getEnv` (optional) or removed entirely without affecting any runtime behaviour.

---

## Algorithm reference

| Property | Value |
|---|---|
| Cipher | AES-256-GCM |
| Key size | 256 bits (32 bytes) |
| IV/Nonce | 96 bits (12 bytes), random per encryption |
| Auth tag | 128 bits (16 bytes) |
| KDF (Phase 2) | PBKDF2-HMAC-SHA256, 100,000 iterations |
