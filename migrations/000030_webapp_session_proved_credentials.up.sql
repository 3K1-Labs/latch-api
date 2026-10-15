-- LATCH_BACKEND_SIGNER_IDENTITY.md: which credentials has *this session*
-- actually proved via a WebAuthn ceremony (authentication/registration/attach
-- finish), independent of the sid cookie's user_id. Authorization for
-- signer-changing and account-listing routes must check this set, not the
-- cookie user.
CREATE TABLE webapp.session_proved_credentials (
  session_id    UUID NOT NULL REFERENCES webapp.sessions(id) ON DELETE CASCADE,
  credential_id TEXT NOT NULL,
  proved_at     BIGINT NOT NULL,
  PRIMARY KEY (session_id, credential_id)
);

CREATE INDEX idx_webapp_session_proved_credentials_session_id
  ON webapp.session_proved_credentials(session_id);
