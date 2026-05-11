CREATE TABLE vote_results (
    poll_id    TEXT NOT NULL,
    choice     TEXT NOT NULL,
    count      INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (poll_id, choice)
);
