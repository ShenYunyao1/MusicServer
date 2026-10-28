package music

import (
	"net/http"
	"strings"
)

func (a *API) tagBatchesHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v2/tag-batches" && !strings.HasPrefix(r.URL.Path, "/api/v2/tag-batches/") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if !a.requireAccount(w, r) {
		return true
	}
	if err := requireTagsSchema(a.store.db); err != nil {
		writeTagError(w, err)
		return true
	}
	if !tagNoQuery(w, r) {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v2/"), "/")
	auth := authFromRequest(r)
	if len(parts) == 1 {
		if r.Method == "GET" {
			items, err := a.store.ListTagBatches(auth)
			if err != nil {
				writeTagError(w, err)
				return true
			}
			writeJSON(w, 200, items)
			return true
		}
		if r.Method != "POST" {
			tagMethod(w, "GET, POST")
			return true
		}
		input, ok := tagJSON[TagBatchInput](w, r)
		if !ok {
			return true
		}
		job, err := a.store.PreviewTagBatch(auth, input)
		if err != nil {
			writeTagError(w, err)
			return true
		}
		tagEntity(w, 201, job.Version, job)
		return true
	}
	if len(parts) > 3 || !tagUUID(parts[1]) {
		writeTagError(w, errResourceHidden)
		return true
	}
	if len(parts) == 2 && r.Method == "GET" {
		job, err := a.store.GetTagBatch(auth, parts[1])
		if err != nil {
			writeTagError(w, err)
			return true
		}
		tagEntity(w, 200, job.Version, job)
		return true
	}
	if len(parts) == 2 && r.Method != "PATCH" {
		tagMethod(w, "GET, PATCH")
		return true
	}
	if len(parts) == 3 && parts[2] != "start" && parts[2] != "cancel" && parts[2] != "retry" {
		writeTagError(w, errResourceHidden)
		return true
	}
	if len(parts) == 3 && r.Method != "POST" {
		tagMethod(w, "POST")
		return true
	}
	version, ok := tagExpectedVersion(w, r)
	if !ok {
		return true
	}
	var job TagBatch
	var err error
	if len(parts) == 2 {
		input, ok := tagJSON[struct {
			AddIDs    []string `json:"add_ids"`
			RemoveIDs []string `json:"remove_ids"`
		}](w, r)
		if !ok {
			return true
		}
		job, err = a.store.EditTagBatch(auth, parts[1], version, input.AddIDs, input.RemoveIDs)
	} else {
		if r.ContentLength != 0 {
			if _, ok := tagJSON[struct{}](w, r); !ok {
				return true
			}
		}
		switch parts[2] {
		case "start":
			job, err = a.store.StartTagBatch(auth, parts[1], version)
		case "cancel":
			job, err = a.store.CancelTagBatch(auth, parts[1], version)
		case "retry":
			job, err = a.store.RetryTagBatch(auth, parts[1], version)
		}
	}
	if err != nil {
		writeTagError(w, err)
		return true
	}
	tagEntity(w, 200, job.Version, job)
	return true
}
