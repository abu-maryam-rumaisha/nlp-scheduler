-- Two-factor authentication with an authenticator app (TOTP). A user has 2FA
-- on when totp_secret is set. totp_last_step is the time step of the last code
-- accepted, so a code cannot be replayed.
ALTER TABLE users
    ADD COLUMN totp_secret    TEXT,
    ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
