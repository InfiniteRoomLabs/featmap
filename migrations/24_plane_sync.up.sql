CREATE TYPE plane_comment_origin AS ENUM ('featmap', 'plane');
CREATE TYPE plane_sync_status AS ENUM ('ok', 'error', 'pending');

CREATE TABLE plane_connections (
    workspace_id     uuid NOT NULL,
    id               uuid NOT NULL,
    project_id       uuid NOT NULL,
    base_url         text NOT NULL,
    plane_workspace  text NOT NULL,
    api_key_cipher   bytea NOT NULL,
    api_key_nonce    bytea NOT NULL,
    api_key_hint     text NOT NULL,
    watched_projects text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL,
    last_modified    timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, project_id)
);

CREATE TABLE plane_links (
    workspace_id       uuid NOT NULL,
    id                 uuid NOT NULL,
    project_id         uuid NOT NULL,
    feature_id         uuid NOT NULL,
    plane_project_id   text NOT NULL,
    plane_work_item_id text NOT NULL,
    last_pulled_cursor text NOT NULL DEFAULT '',
    last_synced_at     timestamptz,
    last_status        plane_sync_status NOT NULL DEFAULT 'pending',
    last_error         text NOT NULL DEFAULT '',
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, feature_id)
);

CREATE TABLE plane_comment_map (
    workspace_id       uuid NOT NULL,
    id                 uuid NOT NULL,
    link_id            uuid NOT NULL,
    featmap_comment_id uuid,
    plane_comment_id   text,
    origin             plane_comment_origin NOT NULL,
    plane_updated_at   timestamptz,
    created_at         timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, plane_comment_id)
);
