package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const tagNameRunes = 64
const tagAliasLimit = 32
const publicTagLimit = 10000
const personalTagLimit = 2000

func normalizeTagName(value string) (string, string, error) {
	if !utf8.ValidString(value) || len(value) > 1024 {
		return "", "", errTagInvalid
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", "", errTagInvalid
		}
	}
	display := norm.NFC.String(strings.Join(strings.Fields(value), " "))
	if display == "" || utf8.RuneCountInString(display) > tagNameRunes {
		return "", "", errTagInvalid
	}
	return display, norm.NFC.String(cases.Fold().String(display)), nil
}

func requireTagsSchema(reader sqlReader) error {
	schema, err := sqliteSchemaIdentity(reader)
	if err != nil || schema != 8 {
		return errTagsUnavailable
	}
	var count int
	if err := reader.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('tag_groups','tag_scopes','tags','tag_aliases','track_tags','track_tag_overrides','track_tag_states','saved_track_filters')").Scan(&count); err != nil || count != 8 {
		return errTagsUnavailable
	}
	return nil
}

func (s *Store) tagTransaction(notify bool, run func(*sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := requireTagsSchema(tx); err != nil {
		return err
	}
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if notify {
		s.notifyCommunityCommit()
	}
	return nil
}

func tagActor(reader sqlReader, auth authContext) (User, error) {
	if !auth.session || auth.userID == "" || auth.keyID == "" {
		return User{}, errTagSession
	}
	actor, err := readUser(reader, auth.userID)
	if err != nil || !actor.Enabled || actor.DeletedAt != "" || actor.MustChangePassword || actor.AuthVersion != auth.authVersion {
		return User{}, errTagSession
	}
	var sessionVersion int64
	var active, expires string
	if err := reader.QueryRow("SELECT auth_version,last_active_at,expires_at FROM sessions WHERE id=? AND user_id=? AND revoked_at=''", auth.keyID, auth.userID).Scan(&sessionVersion, &active, &expires); err != nil || sessionVersion != actor.AuthVersion {
		return User{}, errTagSession
	}
	lastActive, err := parsePersistedTime(active)
	if err != nil {
		return User{}, errTagSession
	}
	expiresAt, err := parsePersistedTime(expires)
	now := time.Now()
	if err != nil || !now.Before(expiresAt) || !now.Before(lastActive.Add(sessionIdle)) {
		return User{}, errTagSession
	}
	return actor, nil
}

func tagScopeID(userID, scope string) (string, error) {
	switch scope {
	case "", "public":
		return "public", nil
	case "personal":
		if userID != "" && userID != "public" {
			return userID, nil
		}
	}
	return "", errTagInvalid
}

func ensureTagScope(tx *sql.Tx, userID string) error {
	if userID == "" || userID == "public" {
		return errTagInvalid
	}
	_, err := tx.Exec("INSERT INTO tag_scopes(id,user_id,revision) VALUES(?,?,0) ON CONFLICT(id) DO NOTHING", userID, userID)
	return err
}

func bumpTagScope(tx *sql.Tx, scopeID string) error {
	result, err := tx.Exec("UPDATE tag_scopes SET revision=revision+1 WHERE id=?", scopeID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errTagInvalid
	}
	return nil
}

func readTag(reader sqlReader, id string) (Tag, error) {
	var tag Tag
	err := reader.QueryRow("SELECT id,scope_id,group_id,name,version,deleted,COALESCE(merged_into,''),created_at,updated_at FROM tags WHERE id=?", id).Scan(&tag.ID, &tag.scopeID, &tag.GroupID, &tag.Name, &tag.Version, &tag.Deleted, &tag.MergedInto, &tag.CreatedAt, &tag.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, errResourceHidden
	}
	if err != nil {
		return Tag{}, err
	}
	tag.Scope = "personal"
	if tag.scopeID == "public" {
		tag.Scope = "public"
	}
	tag.Aliases = []string{}
	rows, err := reader.Query("SELECT display_name FROM tag_aliases WHERE tag_id=? AND is_primary=0 ORDER BY normalized_name", id)
	if err != nil {
		return Tag{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return Tag{}, err
		}
		tag.Aliases = append(tag.Aliases, alias)
	}
	return tag, rows.Err()
}

func authorizeTag(actor User, tag Tag, write bool) error {
	if tag.scopeID != "public" && tag.scopeID != actor.ID {
		return errResourceHidden
	}
	if write && tag.scopeID == "public" && actor.Role != "admin" {
		return errTagForbidden
	}
	return nil
}

func auditTagChange(tx *sql.Tx, auth authContext, scopeID, action, id string, before, after any) error {
	if scopeID != "public" {
		// Private dictionary spelling, aliases, and linked content never enter
		// the administrator-visible audit stream.
		before = nil
		after = map[string]string{"scope": "personal"}
		id = scopeID
	}
	return auditMutation(tx, action, id, before, after, []MutationOptions{{Actor: auth.username, ActorType: "session", UserID: auth.userID, CredentialID: auth.keyID}})
}

func tagNameError(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return errTagNameTaken
	}
	return err
}

func expectedTagVersion(current, expected int64) error {
	if expected < 1 {
		return errTagInvalid
	}
	if current != expected {
		return errVersionConflict
	}
	return nil
}

func readTagGroup(reader sqlReader, id string) (TagGroup, error) {
	var group TagGroup
	err := reader.QueryRow("SELECT id,name,sort_order,builtin,version,created_at,updated_at FROM tag_groups WHERE id=?", id).Scan(&group.ID, &group.Name, &group.SortOrder, &group.Builtin, &group.Version, &group.CreatedAt, &group.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return group, errResourceHidden
	}
	return group, err
}

func (s *Store) ListTagGroups(auth authContext) ([]TagGroup, error) {
	groups := []TagGroup{}
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		if _, err := tagActor(tx, auth); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT id,name,sort_order,builtin,version,created_at,updated_at FROM tag_groups ORDER BY sort_order,id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var group TagGroup
			if err := rows.Scan(&group.ID, &group.Name, &group.SortOrder, &group.Builtin, &group.Version, &group.CreatedAt, &group.UpdatedAt); err != nil {
				return err
			}
			groups = append(groups, group)
		}
		return rows.Err()
	})
	return groups, err
}

func (s *Store) CreateTagGroup(auth authContext, input TagGroupInput) (TagGroup, error) {
	var group TagGroup
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if actor.Role != "admin" {
			return errTagForbidden
		}
		name, normalized, err := normalizeTagName(input.Name)
		if err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM tag_groups").Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return errTagLimit
		}
		now := persistedTime(time.Now())
		group = TagGroup{ID: uuid.NewString(), Name: name, SortOrder: input.SortOrder, Version: 1, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec("INSERT INTO tag_groups VALUES(?,?,?,?,0,1,?,?)", group.ID, name, normalized, group.SortOrder, now, now); err != nil {
			return tagNameError(err)
		}
		if err := bumpTagScope(tx, "public"); err != nil {
			return err
		}
		return auditTagChange(tx, auth, "public", "tag_group.create", group.ID, nil, group)
	})
	return group, err
}

func (s *Store) UpdateTagGroup(auth authContext, id string, patch TagGroupPatch, expectedVersion int64) (TagGroup, error) {
	var result TagGroup
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if actor.Role != "admin" {
			return errTagForbidden
		}
		group, err := readTagGroup(tx, id)
		if err != nil {
			return err
		}
		if err := expectedTagVersion(group.Version, expectedVersion); err != nil {
			return err
		}
		if patch.Name == nil && patch.SortOrder == nil {
			return errTagInvalid
		}
		before := group
		if patch.Name != nil {
			group.Name = *patch.Name
		}
		if patch.SortOrder != nil {
			group.SortOrder = *patch.SortOrder
		}
		name, normalized, err := normalizeTagName(group.Name)
		if err != nil {
			return err
		}
		group.Name = name
		if group.Name == before.Name && group.SortOrder == before.SortOrder {
			result = before
			return nil
		}
		group.Version++
		group.UpdatedAt = persistedTime(time.Now())
		if _, err := tx.Exec("UPDATE tag_groups SET name=?,normalized_name=?,sort_order=?,version=?,updated_at=? WHERE id=?", name, normalized, group.SortOrder, group.Version, group.UpdatedAt, id); err != nil {
			return tagNameError(err)
		}
		if err := bumpTagScope(tx, "public"); err != nil {
			return err
		}
		result = group
		return auditTagChange(tx, auth, "public", "tag_group.update", id, before, group)
	})
	return result, err
}

func (s *Store) DeleteTagGroup(auth authContext, id string, expectedVersion int64) error {
	return s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		if actor.Role != "admin" {
			return errTagForbidden
		}
		group, err := readTagGroup(tx, id)
		if err != nil {
			return err
		}
		if err := expectedTagVersion(group.Version, expectedVersion); err != nil {
			return err
		}
		if group.Builtin {
			return errTagInUse
		}
		var used bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM tags WHERE group_id=?)", id).Scan(&used); err != nil {
			return err
		}
		if used {
			return errTagInUse
		}
		if _, err := tx.Exec("DELETE FROM tag_groups WHERE id=?", id); err != nil {
			return err
		}
		if err := bumpTagScope(tx, "public"); err != nil {
			return err
		}
		return auditTagChange(tx, auth, "public", "tag_group.delete", id, group, nil)
	})
}

func normalizedTagInput(input TagInput) (TagInput, []string, error) {
	name, normalized, err := normalizeTagName(input.Name)
	if err != nil || len(input.Aliases) > tagAliasLimit {
		return TagInput{}, nil, errTagInvalid
	}
	input.Name = name
	names := []string{normalized}
	aliases := []string{}
	seen := map[string]bool{normalized: true}
	for _, alias := range input.Aliases {
		display, key, err := normalizeTagName(alias)
		if err != nil {
			return TagInput{}, nil, err
		}
		if seen[key] {
			return TagInput{}, nil, errTagInvalid
		}
		seen[key] = true
		aliases = append(aliases, display)
		names = append(names, key)
	}
	input.Aliases = aliases
	return input, names, nil
}

func writeTagNamespace(tx *sql.Tx, tag Tag, names []string) error {
	if _, err := tx.Exec("DELETE FROM tag_aliases WHERE tag_id=?", tag.ID); err != nil {
		return err
	}
	for position, normalized := range names {
		display := tag.Name
		primary := position == 0
		if !primary {
			display = tag.Aliases[position-1]
		}
		if _, err := tx.Exec("INSERT INTO tag_aliases VALUES(?,?,?,?,?,?)", tag.scopeID, tag.GroupID, normalized, tag.ID, display, primary); err != nil {
			return tagNameError(err)
		}
	}
	return nil
}

func (s *Store) CreateTag(auth authContext, input TagInput) (Tag, error) {
	var tag Tag
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		scopeID, err := tagScopeID(actor.ID, input.Scope)
		if err != nil {
			return err
		}
		tag.scopeID = scopeID
		if err := authorizeTag(actor, tag, true); err != nil {
			return err
		}
		if _, err := readTagGroup(tx, input.GroupID); err != nil {
			if errors.Is(err, errResourceHidden) {
				return errTagInvalid
			}
			return err
		}
		normalized, names, err := normalizedTagInput(input)
		if err != nil {
			return err
		}
		if scopeID != "public" {
			if err := ensureTagScope(tx, actor.ID); err != nil {
				return err
			}
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM tags WHERE scope_id=? AND deleted=0", scopeID).Scan(&count); err != nil {
			return err
		}
		limit := personalTagLimit
		if scopeID == "public" {
			limit = publicTagLimit
		}
		if count >= limit {
			return errTagLimit
		}
		now := persistedTime(time.Now())
		scope := "personal"
		if scopeID == "public" {
			scope = "public"
		}
		tag = Tag{ID: uuid.NewString(), scopeID: scopeID, Scope: scope, GroupID: input.GroupID, Name: normalized.Name, Aliases: normalized.Aliases, Version: 1, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec("INSERT INTO tags VALUES(?,?,?,?,?,1,0,NULL,?,?)", tag.ID, scopeID, tag.GroupID, tag.Name, names[0], now, now); err != nil {
			return tagNameError(err)
		}
		if err := writeTagNamespace(tx, tag, names); err != nil {
			return err
		}
		if err := bumpTagScope(tx, scopeID); err != nil {
			return err
		}
		return auditTagChange(tx, auth, scopeID, "tag.create", tag.ID, nil, tag)
	})
	return tag, err
}

func (s *Store) GetTag(auth authContext, id string) (Tag, error) {
	var tag Tag
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		tag, err = readTag(tx, id)
		if err != nil {
			return err
		}
		return authorizeTag(actor, tag, false)
	})
	if err != nil {
		return Tag{}, err
	}
	return tag, nil
}

func (s *Store) UpdateTag(auth authContext, id string, patch TagPatch, expectedVersion int64) (Tag, error) {
	var result Tag
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		tag, err := readTag(tx, id)
		if err != nil {
			return err
		}
		if err := authorizeTag(actor, tag, true); err != nil {
			return err
		}
		if tag.Deleted || tag.MergedInto != "" {
			return errResourceHidden
		}
		if err := expectedTagVersion(tag.Version, expectedVersion); err != nil {
			return err
		}
		if patch.Name == nil && patch.Aliases == nil {
			return errTagInvalid
		}
		input := TagInput{Name: tag.Name, Aliases: tag.Aliases}
		if patch.Name != nil {
			input.Name = *patch.Name
		}
		if patch.Aliases != nil {
			input.Aliases = *patch.Aliases
		}
		normalized, names, err := normalizedTagInput(input)
		if err != nil {
			return err
		}
		before := tag
		oldAliases, _ := json.Marshal(tag.Aliases)
		aliases, _ := json.Marshal(normalized.Aliases)
		if tag.Name == normalized.Name && string(oldAliases) == string(aliases) {
			result = tag
			return nil
		}
		tag.Name = normalized.Name
		tag.Aliases = normalized.Aliases
		tag.Version++
		tag.UpdatedAt = persistedTime(time.Now())
		if _, err := tx.Exec("UPDATE tags SET name=?,normalized_name=?,version=?,updated_at=? WHERE id=?", tag.Name, names[0], tag.Version, tag.UpdatedAt, id); err != nil {
			return tagNameError(err)
		}
		if err := writeTagNamespace(tx, tag, names); err != nil {
			return err
		}
		if err := bumpTagScope(tx, tag.scopeID); err != nil {
			return err
		}
		result, err = readTag(tx, id)
		if err != nil {
			return err
		}
		return auditTagChange(tx, auth, tag.scopeID, "tag.update", id, before, tag)
	})
	return result, err
}

func (s *Store) DeleteTag(auth authContext, id string, expectedVersion int64) error {
	return s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		tag, err := readTag(tx, id)
		if err != nil {
			return err
		}
		if err := authorizeTag(actor, tag, true); err != nil {
			return err
		}
		if tag.Deleted || tag.MergedInto != "" {
			return errResourceHidden
		}
		if err := expectedTagVersion(tag.Version, expectedVersion); err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM track_tags WHERE tag_id=?) OR EXISTS(SELECT 1 FROM track_tag_overrides WHERE tag_id=?) OR EXISTS(SELECT 1 FROM tags WHERE merged_into=?) OR EXISTS(SELECT 1 FROM saved_track_filters f,json_tree(f.query_json) j WHERE j.type='text' AND j.value=?)`, id, id, id, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return errTagInUse
		}
		if _, err := tx.Exec("UPDATE tags SET deleted=1,version=version+1,updated_at=? WHERE id=?", persistedTime(time.Now()), id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM tag_aliases WHERE tag_id=?", id); err != nil {
			return err
		}
		if err := bumpTagScope(tx, tag.scopeID); err != nil {
			return err
		}
		return auditTagChange(tx, auth, tag.scopeID, "tag.delete", id, tag, nil)
	})
}

func (s *Store) ListTags(auth authContext, query TagQuery) (TagPage, error) {
	page := TagPage{Items: []Tag{}, Offset: query.Offset, Limit: query.Limit}
	if page.Limit == 0 {
		page.Limit = 100
	}
	if page.Offset < 0 || page.Offset > 1000000 || page.Limit < 1 || page.Limit > 100 {
		return page, errTagInvalid
	}
	err := s.tagTransaction(false, func(tx *sql.Tx) error {
		actor, err := tagActor(tx, auth)
		if err != nil {
			return err
		}
		scopeID, err := tagScopeID(actor.ID, query.Scope)
		if err != nil {
			return err
		}
		if query.GroupID != "" {
			if _, err := readTagGroup(tx, query.GroupID); err != nil {
				if errors.Is(err, errResourceHidden) {
					return errTagInvalid
				}
				return err
			}
		}
		search := ""
		if query.Query != "" {
			_, search, err = normalizeTagName(query.Query)
			if err != nil {
				return err
			}
		}
		where := " FROM tags t WHERE t.scope_id=? AND t.deleted=0"
		args := []any{scopeID}
		if query.GroupID != "" {
			where += " AND t.group_id=?"
			args = append(args, query.GroupID)
		}
		if search != "" {
			where += " AND EXISTS(SELECT 1 FROM tag_aliases a WHERE a.tag_id=t.id AND instr(a.normalized_name,?)>0)"
			args = append(args, search)
		}
		if err := tx.QueryRow("SELECT count(*)"+where, args...).Scan(&page.Total); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COALESCE((SELECT revision FROM tag_scopes WHERE id=?),0)", scopeID).Scan(&page.Revision); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT t.id"+where+" ORDER BY t.normalized_name,t.id LIMIT ? OFFSET ?", append(args, page.Limit, page.Offset)...)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			tag, err := readTag(tx, id)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, tag)
		}
		return nil
	})
	return page, err
}
