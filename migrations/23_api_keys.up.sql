CREATE TABLE api_keys
(
    id            uuid                     NOT NULL,
    account_id    uuid                     NOT NULL,
    name          varchar                  NOT NULL,
    key_hash      varchar                  NOT NULL,
    key_prefix    varchar                  NOT NULL,
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL,
    last_used_at  TIMESTAMP WITH TIME ZONE,
    expires_at    TIMESTAMP WITH TIME ZONE,
    revoked_at    TIMESTAMP WITH TIME ZONE,
    CONSTRAINT "PK_api_keys_1" PRIMARY KEY (id),
    CONSTRAINT "FK_api_keys_account" FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE,
    CONSTRAINT "UN_api_keys_hash" UNIQUE (key_hash)
)
    WITH (
        OIDS = FALSE
    );

CREATE INDEX api_keys_account_id_idx ON api_keys (account_id) WHERE revoked_at IS NULL;
