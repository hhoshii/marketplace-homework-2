CREATE TYPE user_role AS ENUM ('USER','SELLER','ADMIN');

CREATE TABLE users (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 email VARCHAR(254) NOT NULL UNIQUE,
 password_hash TEXT NOT NULL,
 role user_role NOT NULL DEFAULT 'USER',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE refresh_tokens (
 id UUID PRIMARY KEY,
 user_id UUID NOT NULL REFERENCES users(id),
 token_hash BYTEA NOT NULL UNIQUE,
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens(user_id);
