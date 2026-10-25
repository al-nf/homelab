CREATE TABLE IF NOT EXISTS companies (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    slug         TEXT NOT NULL,
    ats_type     TEXT NOT NULL CHECK(ats_type IN ('greenhouse', 'lever', 'ashby', 'apple', 'google', 'workday', 'generic')),
    last_checked DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_companies_slug ON companies(slug);

CREATE TABLE IF NOT EXISTS postings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id  INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    title       TEXT NOT NULL,
    url         TEXT NOT NULL,
    location    TEXT,
    posted_at   DATETIME,
    found_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_postings_dedup ON postings(company_id, external_id);
CREATE INDEX IF NOT EXISTS idx_postings_found_at ON postings(found_at);
