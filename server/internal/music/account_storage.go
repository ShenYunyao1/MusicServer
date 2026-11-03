package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

type accountStorageView struct {
	AccountStorage
	Email          string `json:"email,omitempty"`
	AccountVersion int64  `json:"account_version"`
	QuotaSource    string `json:"quota_source,omitempty"`
}

type accountStoragePage struct {
	Items  []accountStorageView `json:"items"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
}

// Account-scoped derived references include audio and deduplicated attachments.
// No filesystem walks or full song payloads are needed on request paths.
func (s *Store) accountUsageForIDs(reader sqlReader, ids []string) (map[string]int64, error) {
	used := map[string]int64{}
	for _, id := range ids {
		breakdown, err := accountStorageBreakdown(reader, id)
		if err != nil {
			return nil, err
		}
		used[id] = breakdown.total()
	}
	return used, nil
}

func (s *Store) accountStorageRows(reader sqlReader, where string, args []any, offset, limit int) (accountStoragePage, error) {
	page := accountStoragePage{Items: []accountStorageView{}, Offset: offset, Limit: limit}
	if err := reader.QueryRow("SELECT count(*) FROM users u WHERE "+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	query := `SELECT u.id,u.username,u.version,EXISTS(SELECT 1 FROM meta WHERE key='deleted_user/'||u.id),COALESCE(q.value,'{}'),COALESCE(e.value,'') FROM users u LEFT JOIN settings q ON q.key='storage_quota/'||u.id LEFT JOIN meta e ON e.key='account_email/'||u.id WHERE ` + where + " ORDER BY u.username,u.id LIMIT ? OFFSET ?"
	params := append(append([]any{}, args...), limit, offset)
	rows, err := reader.Query(query, params...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var item accountStorageView
		var quotaRaw, emailRaw string
		if err := rows.Scan(&item.ID, &item.Username, &item.AccountVersion, &item.Deleted, &quotaRaw, &emailRaw); err != nil {
			rows.Close()
			return page, err
		}
		if err := json.Unmarshal([]byte(quotaRaw), &item.StorageQuota); err != nil {
			rows.Close()
			return page, err
		}
		email, err := decodeAccountEmail(emailRaw)
		if err != nil {
			rows.Close()
			return page, err
		}
		item.Email, item.QuotaSource = email.Email, email.QuotaSource
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	for i := range page.Items {
		b, err := accountStorageBreakdown(reader, page.Items[i].ID)
		if err != nil {
			return page, err
		}
		page.Items[i].storageBreakdown = b
		page.Items[i].UsedBytes = b.total()
	}
	return page, nil
}

func (a *API) accountStorage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return
	}
	if r.Method != "GET" {
		methodNotAllowed(w, r)
		return
	}
	auth := authFromRequest(r)
	self := r.URL.Path == "/api/v2/auth/storage"
	if !self && !a.requireAdmin(w, r) {
		return
	}
	where := "u.id=?"
	args := []any{auth.userID}
	offset, limit := 0, 1
	if !self {
		values := r.URL.Query()
		if values.Get("deleted") == "true" {
			if values.Has("ids") {
				writeError(w, 400, "INVALID_ACCOUNT_PAGE", "ids cannot be combined with deleted history")
				return
			}
			where = "EXISTS(SELECT 1 FROM meta WHERE key='deleted_user/'||u.id)"
			args = nil
			limit = 50
			for key, dest := range map[string]*int{"offset": &offset, "limit": &limit} {
				if values.Has(key) {
					n, err := strconv.Atoi(values.Get(key))
					if err != nil || n < 0 || key == "limit" && (n < 1 || n > 100) {
						writeError(w, 400, "INVALID_ACCOUNT_PAGE", "invalid account page")
						return
					}
					*dest = n
				}
			}
		} else {
			ids := strings.Split(values.Get("ids"), ",")
			if len(ids) > 100 || values.Get("ids") == "" {
				writeError(w, 400, "INVALID_ACCOUNT_IDS", "supply between 1 and 100 account IDs")
				return
			}
			seen := map[string]bool{}
			args = nil
			for _, id := range ids {
				if !validID.MatchString(id) || seen[id] {
					writeError(w, 400, "INVALID_ACCOUNT_IDS", "invalid or repeated account ID")
					return
				}
				seen[id] = true
				args = append(args, id)
			}
			where = "u.id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
			limit = len(ids)
		}
	}
	var page accountStoragePage
	validate := func(tx *sql.Tx) error {
		if err := validateAccountSession(tx, auth); err != nil {
			return err
		}
		if !self {
			if err := requireAdminActor(tx, []MutationOptions{mutationOptions(r)}); err != nil {
				return err
			}
		}
		return nil
	}
	err := a.store.readSnapshot(r.Context(), func(tx *sql.Tx) error {
		if err := validate(tx); err != nil {
			return err
		}
		var err error
		page, err = a.store.accountStorageRows(tx, where, args, offset, limit)
		return err
	})
	// Revocation during slow media-disk I/O must still prevent disclosure.
	if err == nil {
		err = a.store.readSnapshot(r.Context(), validate)
	}
	if err != nil {
		if errors.Is(err, errResourceHidden) {
			writeError(w, 401, "AUTH_INVALID", "sign in again")
		} else {
			writeError(w, 500, "ACCOUNT_STORAGE_FAILED", "无法读取账户容量")
		}
		return
	}
	if self {
		if len(page.Items) != 1 {
			writeError(w, 404, "NOT_FOUND", "account unavailable")
			return
		}
		writeJSON(w, 200, page.Items[0])
		return
	}
	writeJSON(w, 200, page)
}
