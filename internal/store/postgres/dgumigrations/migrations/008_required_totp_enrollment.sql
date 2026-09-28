ALTER TABLE user_auth_challenges DROP CONSTRAINT user_auth_challenges_purpose_check;
ALTER TABLE user_auth_challenges ADD CONSTRAINT user_auth_challenges_purpose_check
    CHECK (purpose IN ('totp', 'password_change', 'enrollment'));
ALTER TABLE user_totp ADD COLUMN lockout_level INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_totp ADD COLUMN locked_until TIMESTAMPTZ;

---- create above / drop below ----
ALTER TABLE user_totp DROP COLUMN locked_until;
ALTER TABLE user_totp DROP COLUMN lockout_level;
DELETE FROM user_auth_challenges WHERE purpose = 'enrollment';
ALTER TABLE user_auth_challenges DROP CONSTRAINT user_auth_challenges_purpose_check;
ALTER TABLE user_auth_challenges ADD CONSTRAINT user_auth_challenges_purpose_check
    CHECK (purpose IN ('totp', 'password_change'));
