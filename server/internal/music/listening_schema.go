package music

// Listening data is private to its account and is not a media ownership asset.
const listeningSchema = `
CREATE TABLE listening_sessions (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL CHECK(length(session_id)=36),
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 started_at INTEGER NOT NULL,played_ms INTEGER NOT NULL CHECK(played_ms>=0),
 minimum_ms INTEGER NOT NULL CHECK(minimum_ms>0),
 counted INTEGER NOT NULL DEFAULT 0 CHECK(counted IN(0,1)),last_seen_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,session_id));
CREATE INDEX listening_sessions_expiry ON listening_sessions(user_id,last_seen_at);
CREATE TABLE listening_totals (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 play_count INTEGER NOT NULL CHECK(play_count>0),last_played_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,track_id));
CREATE INDEX listening_totals_order ON listening_totals(user_id,play_count DESC,last_played_at DESC,track_id);
CREATE TABLE listening_daily (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 day TEXT NOT NULL,play_count INTEGER NOT NULL CHECK(play_count>0),last_played_at INTEGER NOT NULL,
 PRIMARY KEY(user_id,track_id,day));
CREATE INDEX listening_daily_range ON listening_daily(user_id,day,track_id);
`
