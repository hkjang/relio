-- The personal area advertised 저장된 검색 and 즐겨찾기 in the navigation but both
-- were placeholder screens. These give them real storage.

-- A saved view is the querystring a user arrived at, named and kept. The server
-- treats the query as opaque text: it is replayed against the list endpoint,
-- which applies the caller's Data Scope exactly as it would for a fresh request.
CREATE TABLE user_saved_views (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource text NOT NULL CHECK (resource IN ('CUSTOMER', 'OPPORTUNITY', 'VOICE', 'ACTIVITY', 'CONTRACT')),
    name text NOT NULL,
    query text NOT NULL DEFAULT '',
    -- Pinned views appear in the sidebar of their list screen.
    pinned boolean NOT NULL DEFAULT false,
    display_order integer NOT NULL DEFAULT 100,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, resource, name)
);
CREATE INDEX user_saved_views_user_idx ON user_saved_views(user_id, resource, display_order);

-- Favorites are a per-user pointer only. The referenced record is always
-- re-read through the scoped list query, so a record that moves out of the
-- user's Data Scope stops appearing without needing a cleanup job.
CREATE TABLE user_favorites (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    resource text NOT NULL CHECK (resource IN ('CUSTOMER', 'OPPORTUNITY', 'VOICE', 'CONTRACT')),
    resource_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, resource, resource_id)
);
CREATE INDEX user_favorites_user_idx ON user_favorites(user_id, created_at DESC);
