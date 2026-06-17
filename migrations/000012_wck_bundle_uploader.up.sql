-- Bind each WCK bundle to the principal that first uploaded it so a stranger
-- who knows the (public) C-address-derived pickup key cannot overwrite a
-- wallet's bundle with a poisoned key. Existing rows (dev/testnet only) get ''
-- and stay overwritable by their next legitimate upload.
ALTER TABLE wck_bundles ADD COLUMN uploader TEXT NOT NULL DEFAULT '';
