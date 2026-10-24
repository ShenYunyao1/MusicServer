package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
)

const realtimeCursorTTL = 5 * time.Minute
const realtimeCursorLimit = 64
const realtimeRecordLimit = 5000

type realtimeView struct {
	Catalog            bool   `json:"catalog"`
	Query              string `json:"query"`
	Offset             int    `json:"offset"`
	Subscriptions      bool   `json:"subscriptions"`
	SubscriptionDetail string `json:"subscription_detail"`
	Invitations        bool   `json:"invitations"`
	InvitationID       string `json:"invitation_id"`
	InvitationOffset   int    `json:"invitation_offset"`
	OwnerPlaylist      string `json:"owner_playlist"`
	OwnerOffset        int    `json:"owner_offset"`
	PublicDetail       string `json:"public_detail"`
}

func (v realtimeView) valid() bool {
	if len(v.Query) > 1024 || v.Offset < 0 || v.InvitationOffset < 0 || v.OwnerOffset < 0 || v.Offset > 1000000 || v.InvitationOffset > 1000000 || v.OwnerOffset > 1000000 {
		return false
	}
	for _, id := range []string{v.InvitationID, v.OwnerPlaylist, v.PublicDetail, v.SubscriptionDetail} {
		if id != "" && !validID.MatchString(id) {
			return false
		}
	}
	return true
}

type realtimeSection struct {
	Rows  map[string]json.RawMessage
	Order []string
	Meta  map[string]any
}

func newRealtimeSection(meta map[string]any) *realtimeSection {
	return &realtimeSection{Rows: map[string]json.RawMessage{}, Order: []string{}, Meta: meta}
}
func (s *realtimeSection) add(id string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.Rows[id] = raw
	s.Order = append(s.Order, id)
	return nil
}

type realtimeSnapshot map[string]*realtimeSection
type realtimeIndex struct {
	Rows     map[string]string
	Digest   string
	Overflow bool
}
type realtimeBaseline struct {
	Binding  string
	View     string
	Expires  time.Time
	Sections map[string]realtimeIndex
	Digest   string
}
type realtimeDelta struct {
	Reset   bool              `json:"reset,omitempty"`
	Changed []json.RawMessage `json:"changed"`
	Removed []string          `json:"removed"`
	Order   []string          `json:"order"`
	Meta    map[string]any    `json:"meta"`
}
type realtimeReply struct {
	Cursor   string                   `json:"cursor"`
	Reset    bool                     `json:"reset"`
	Sections map[string]realtimeDelta `json:"sections"`
}

func realtimeBinding(auth authContext) string {
	raw, _ := json.Marshal([]any{auth.userID, auth.keyID, auth.authVersion})
	return hashBytes(raw)
}
func realtimeViewKey(view realtimeView) string { raw, _ := json.Marshal(view); return string(raw) }
func indexRealtime(snapshot realtimeSnapshot) (map[string]realtimeIndex, string) {
	indexes := map[string]realtimeIndex{}
	for name, section := range snapshot {
		rows := map[string]string{}
		for id, raw := range section.Rows {
			rows[id] = hashBytes(raw)
		}
		raw, _ := json.Marshal([]any{rows, section.Order, section.Meta})
		index := realtimeIndex{Rows: rows, Digest: hashBytes(raw)}
		if len(rows) > realtimeRecordLimit {
			index.Rows = nil
			index.Overflow = true
		}
		indexes[name] = index
	}
	raw, _ := json.Marshal(indexes)
	return indexes, hashBytes(raw)
}

// Projections use the same ownership and privacy rules as the ordinary endpoints.
// The next comparison reads authoritative data again; cursors contain no records.
func (s *Store) realtimeSnapshot(auth authContext, view realtimeView) (realtimeSnapshot, error) {
	snapshot := realtimeSnapshot{}
	var pending int
	if err := s.db.QueryRow("SELECT count(*) FROM playlist_invitations WHERE recipient_id=? AND state='pending'", auth.userID).Scan(&pending); err != nil {
		return nil, err
	}
	snapshot["badge"] = newRealtimeSection(map[string]any{"pending": pending})
	if view.Catalog {
		page, _, _, err := s.publicCatalog(view.Query, view.Offset, 30, "")
		if err != nil {
			return nil, err
		}
		section := newRealtimeSection(map[string]any{"total": page.Total, "offset": page.Offset, "limit": page.Limit})
		for _, p := range page.Items {
			if err := section.add(p.ID, p); err != nil {
				return nil, err
			}
		}
		snapshot["catalog"] = section
	}
	if view.Subscriptions {
		items, err := s.Subscriptions(auth.userID)
		if err != nil {
			return nil, err
		}
		section := newRealtimeSection(map[string]any{})
		for _, p := range items {
			if err := section.add(p.ID, p); err != nil {
				return nil, err
			}
		}
		snapshot["subscriptions"] = section
	}
	if view.Invitations {
		page, err := s.Invitations(auth.userID, "", view.InvitationOffset, 30)
		if view.InvitationID != "" {
			var i PlaylistInvitation
			i, err = s.ReceivedInvitation(view.InvitationID, auth.userID)
			page = InvitationPage{Items: []PlaylistInvitation{}, Limit: 30}
			if err == nil {
				page.Items = append(page.Items, i)
				page.Total = 1
			}
		}
		if errors.Is(err, errResourceHidden) || errors.Is(err, sql.ErrNoRows) {
			page = InvitationPage{Items: []PlaylistInvitation{}, Limit: 30}
			err = nil
		}
		if err != nil {
			return nil, err
		}
		section := newRealtimeSection(map[string]any{"total": page.Total, "offset": page.Offset, "limit": page.Limit})
		for _, i := range page.Items {
			if err := section.add(i.ID, i); err != nil {
				return nil, err
			}
		}
		snapshot["invitations"] = section
	}
	if view.SubscriptionDetail != "" {
		tracks := newRealtimeSection(map[string]any{})
		err := s.transaction(func(tx *sql.Tx) error {
			p, err := subscriptionView(tx, auth.userID, view.SubscriptionDetail)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if p.SubscriptionStatus != "active" {
				return nil
			}
			for _, id := range p.TrackIDs {
				track, err := readTrackFrom(tx, id, true)
				if err != nil {
					return err
				}
				if track.Removed || track.Missing {
					track = unavailableTrack(track)
				}
				if err := tracks.add(id, publicTrack(track, auth)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		snapshot["subscription_tracks"] = tracks
	}
	if view.OwnerPlaylist != "" {
		playlist := newRealtimeSection(map[string]any{"available": false})
		invitations := newRealtimeSection(map[string]any{"total": 0, "offset": view.OwnerOffset, "limit": 30})
		p, err := readPlaylistFrom(s.db, view.OwnerPlaylist)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && p.OwnerID == auth.userID && !p.System {
			page, err := s.Invitations(auth.userID, p.ID, view.OwnerOffset, 30)
			if err != nil && !errors.Is(err, errResourceHidden) {
				return nil, err
			}
			if err == nil {
				playlist.Meta["available"] = true
				if err := playlist.add(p.ID, p); err != nil {
					return nil, err
				}
				invitations.Meta = map[string]any{"total": page.Total, "offset": page.Offset, "limit": page.Limit}
				for _, i := range page.Items {
					if err := invitations.add(i.ID, i); err != nil {
						return nil, err
					}
				}
			}
		}
		snapshot["owner_playlist"], snapshot["owner_invitations"] = playlist, invitations
	}
	if view.PublicDetail != "" {
		playlist := newRealtimeSection(map[string]any{"available": false})
		tracks := newRealtimeSection(map[string]any{})
		err := s.transaction(func(tx *sql.Tx) error {
			p, err := readPlaylistFrom(tx, view.PublicDetail)
			if errors.Is(err, sql.ErrNoRows) || err == nil && !publishedPlaylist(tx, p) {
				return nil
			}
			if err != nil {
				return err
			}
			p, err = publicPlaylistView(tx, p, auth.userID)
			if err != nil {
				return err
			}
			for _, id := range p.TrackIDs {
				t, err := readTrackFrom(tx, id, true)
				if err != nil {
					return err
				}
				if t.Removed || t.Missing {
					p.UnavailableTrackIDs = append(p.UnavailableTrackIDs, id)
					t = unavailableTrack(t)
				}
				if err := tracks.add(id, publicTrack(t, authContext{role: "member"})); err != nil {
					return err
				}
			}
			playlist.Meta["available"] = true
			return playlist.add(p.ID, p)
		})
		if err != nil {
			return nil, err
		}
		snapshot["detail_playlist"], snapshot["detail_tracks"] = playlist, tracks
	}
	return snapshot, nil
}
func (a *API) realtimeSync(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	if request.Method != "POST" {
		writeError(response, 405, "METHOD_NOT_ALLOWED", "method not allowed")
		return
	}
	var input struct {
		Cursor string       `json:"cursor"`
		View   realtimeView `json:"view"`
	}
	request.Body = http.MaxBytesReader(response, request.Body, 16<<10)
	if decodeStrict(request, &input) != nil || len(input.Cursor) > 128 || !input.View.valid() {
		writeError(response, 400, "INVALID_REALTIME_VIEW", "invalid realtime view")
		return
	}
	auth := authFromRequest(request)
	snapshot, err := a.store.realtimeSnapshot(auth, input.View)
	if err != nil {
		writeError(response, 500, "REALTIME_FAILED", "unable to read current view")
		return
	}
	indexes, digest := indexRealtime(snapshot)
	now, binding, view := time.Now(), realtimeBinding(auth), realtimeViewKey(input.View)
	a.realtimeMu.Lock()
	if a.realtimeCursors == nil {
		a.realtimeCursors = map[string]realtimeBaseline{}
	}
	for cursor, item := range a.realtimeCursors {
		if !now.Before(item.Expires) {
			delete(a.realtimeCursors, cursor)
		}
	}
	before, found := a.realtimeCursors[input.Cursor]
	reset := !found || before.Binding != binding || before.View != view
	if reset {
		before = realtimeBaseline{}
	}
	reply := realtimeReply{Reset: reset, Sections: map[string]realtimeDelta{}}
	for name, section := range snapshot {
		previous, exists := before.Sections[name]
		if !reset && exists && previous.Digest == indexes[name].Digest {
			continue
		}
		delta := realtimeDelta{Reset: reset || !exists || previous.Overflow, Changed: []json.RawMessage{}, Removed: []string{}, Order: section.Order, Meta: section.Meta}
		for _, id := range section.Order {
			if delta.Reset || previous.Rows[id] != indexes[name].Rows[id] || indexes[name].Overflow {
				delta.Changed = append(delta.Changed, section.Rows[id])
			}
		}
		for id := range previous.Rows {
			if _, exists := section.Rows[id]; !exists {
				delta.Removed = append(delta.Removed, id)
			}
		}
		sort.Strings(delta.Removed)
		reply.Sections[name] = delta
	}
	if !reset && before.Digest == digest {
		reply.Cursor = input.Cursor
		before.Expires = now.Add(realtimeCursorTTL)
		a.realtimeCursors[input.Cursor] = before
	} else {
		for len(a.realtimeCursors) >= realtimeCursorLimit {
			oldest := ""
			for cursor, item := range a.realtimeCursors {
				if oldest == "" || item.Expires.Before(a.realtimeCursors[oldest].Expires) {
					oldest = cursor
				}
			}
			delete(a.realtimeCursors, oldest)
		}
		reply.Cursor, err = randomToken(24)
		if err != nil {
			a.realtimeMu.Unlock()
			writeError(response, 500, "REALTIME_FAILED", "unable to create cursor")
			return
		}
		a.realtimeCursors[reply.Cursor] = realtimeBaseline{Binding: binding, View: view, Expires: now.Add(realtimeCursorTTL), Sections: indexes, Digest: digest}
	}
	a.realtimeMu.Unlock()
	writeJSON(response, 200, reply)
}
