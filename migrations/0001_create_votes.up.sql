CREATE TABLE votes (
    id         BIGSERIAL PRIMARY KEY,
    poll_id    TEXT      NOT NULL,
    choice     TEXT      NOT NULL,
    user_id    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX votes_poll_created_idx ON votes (poll_id, created_at);
