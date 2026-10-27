package music

// This schema is installed only by the private 7 -> 8 migration while the
// released server remains at schema 7. No tag relation lives in generic meta.
const tagSchema = `
CREATE TABLE tag_groups (
 id TEXT PRIMARY KEY,name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 normalized_name TEXT NOT NULL UNIQUE,sort_order INTEGER NOT NULL DEFAULT 0,
 builtin INTEGER NOT NULL CHECK(builtin IN (0,1)),version INTEGER NOT NULL CHECK(version>=1),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX tag_groups_order ON tag_groups(sort_order,id);
INSERT INTO tag_groups VALUES
 ('genre','风格','风格',0,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 ('language','演唱语言','演唱语言',1,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 ('version','版本','版本',2,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 ('mood','情绪','情绪',3,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 ('scene','场景','场景',4,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 ('custom','自定义','自定义',5,1,1,strftime('%Y-%m-%dT%H:%M:%fZ','now'),strftime('%Y-%m-%dT%H:%M:%fZ','now'));
CREATE TABLE tag_scopes (
 id TEXT PRIMARY KEY,user_id TEXT UNIQUE REFERENCES users(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 CHECK((id='public' AND user_id IS NULL) OR (user_id IS NOT NULL AND id=user_id AND id!='public')));
INSERT INTO tag_scopes(id,user_id,revision) VALUES('public',NULL,0);
CREATE TABLE tags (
 id TEXT PRIMARY KEY CHECK(length(id)=36),scope_id TEXT NOT NULL REFERENCES tag_scopes(id) ON DELETE CASCADE,
 group_id TEXT NOT NULL REFERENCES tag_groups(id),name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 normalized_name TEXT NOT NULL,version INTEGER NOT NULL CHECK(version>=1),
 deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN(0,1)),merged_into TEXT,
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(id,scope_id),UNIQUE(id,scope_id,group_id),CHECK(merged_into IS NULL OR (merged_into!=id AND deleted=1)),
 FOREIGN KEY(merged_into,scope_id,group_id) REFERENCES tags(id,scope_id,group_id));
CREATE INDEX tags_scope_group ON tags(scope_id,group_id,deleted,normalized_name,id);
CREATE INDEX tags_merge_target ON tags(merged_into,scope_id,group_id);
CREATE TABLE tag_aliases (
 scope_id TEXT NOT NULL,group_id TEXT NOT NULL,normalized_name TEXT NOT NULL,
 tag_id TEXT NOT NULL,display_name TEXT NOT NULL CHECK(length(display_name) BETWEEN 1 AND 64),
 is_primary INTEGER NOT NULL CHECK(is_primary IN(0,1)),
 PRIMARY KEY(scope_id,group_id,normalized_name),
 FOREIGN KEY(tag_id,scope_id,group_id) REFERENCES tags(id,scope_id,group_id) ON DELETE CASCADE);
CREATE INDEX tag_aliases_tag ON tag_aliases(tag_id,is_primary,normalized_name);
CREATE UNIQUE INDEX tag_aliases_primary ON tag_aliases(tag_id) WHERE is_primary=1;
CREATE TABLE track_tags (
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 tag_id TEXT NOT NULL,scope_id TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN('manual','imported')),
 created_by TEXT NOT NULL REFERENCES users(id),created_at TEXT NOT NULL,
 PRIMARY KEY(track_id,tag_id),FOREIGN KEY(tag_id,scope_id) REFERENCES tags(id,scope_id) ON DELETE CASCADE);
CREATE INDEX track_tags_scope_track ON track_tags(scope_id,track_id,tag_id);
CREATE INDEX track_tags_tag_track ON track_tags(tag_id,track_id);
CREATE INDEX track_tags_creator ON track_tags(created_by);
CREATE TABLE track_tag_overrides (
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 tag_id TEXT NOT NULL,scope_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN('confirmed','suppressed')),
 updated_by TEXT NOT NULL REFERENCES users(id),updated_at TEXT NOT NULL,
 PRIMARY KEY(track_id,tag_id),FOREIGN KEY(tag_id,scope_id) REFERENCES tags(id,scope_id) ON DELETE CASCADE);
CREATE INDEX track_tag_overrides_scope ON track_tag_overrides(scope_id,track_id,tag_id);
CREATE INDEX track_tag_overrides_tag ON track_tag_overrides(tag_id,track_id);
CREATE INDEX track_tag_overrides_actor ON track_tag_overrides(updated_by);
CREATE TABLE track_tag_states (
 track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
 scope_id TEXT NOT NULL REFERENCES tag_scopes(id) ON DELETE CASCADE,
 version INTEGER NOT NULL CHECK(version>=1),PRIMARY KEY(track_id,scope_id));
CREATE INDEX track_tag_states_scope ON track_tag_states(scope_id,track_id);
CREATE TABLE saved_track_filters (
 id TEXT PRIMARY KEY CHECK(length(id)=36),user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 64),query_json TEXT NOT NULL CHECK(json_valid(query_json)),
 version INTEGER NOT NULL CHECK(version>=1),created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE INDEX saved_track_filters_user ON saved_track_filters(user_id,updated_at,id);
`
