package music

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func receiptCall(a *API, u User, id string) *httptest.ResponseRecorder {
	var sid string
	a.store.db.QueryRow("SELECT id FROM sessions WHERE user_id=? ORDER BY last_active_at DESC LIMIT 1", u.ID).Scan(&sid)
	r := httptest.NewRequest("GET", "/api/v2/uploads/receipts/"+id, nil)
	r = r.WithContext(withAuth(r.Context(), authContext{session: true, userID: u.ID, keyID: sid, authVersion: u.AuthVersion, role: u.Role}))
	w := httptest.NewRecorder()
	a.uploadReceipt(w, r)
	return w
}

func TestUploadReceiptNewAndDuplicateOwnership(t *testing.T) {
	a, _ := testAPI(t)
	u, _, _ := loginTestAccount(t, a.store, "admin")
	data := validTestAudio(t)
	first, err := a.store.StageAudio("new.wav", bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	track, dup, err := a.store.CommitUploadStage(first.ID)
	if err != nil || dup {
		t.Fatal(dup, err)
	}
	w := receiptCall(a, u, first.ID)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Track     Track `json:"track"`
		Duplicate bool  `json:"duplicate"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Track.ID != track.ID || result.Duplicate {
		t.Fatal(w.Body)
	}
	_, _, freshToken := loginTestAccount(t, a.store, "admin")
	if r := sessionRequest(a, freshToken, "GET", "/api/v2/uploads/receipts/"+first.ID, ""); r.Code != 200 {
		t.Fatal("new session cannot recover response", r.Code, r.Body)
	}
	key, err := a.store.CreateAPIKey(APIKeyCreateRequest{Name: "receipt forbidden", AllowedPlaylistIDs: []string{"library"}}, MutationOptions{UserID: u.ID})
	if err != nil {
		t.Fatal(err)
	}
	if r := callAPIWithKey(a, "GET", "/api/v2/uploads/receipts/"+first.ID, "", key.Secret); r.Code != 403 {
		t.Fatal("key read receipt", r.Code, r.Body)
	}
	other, _ := readyMember(t, a, "receipt-reader")
	if w := receiptCall(a, other, first.ID); w.Code != 404 {
		t.Fatal("foreign read", w.Code)
	}
	second, err := a.store.StageAudio("duplicate.wav", bytes.NewReader(data), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, dup, err := a.store.CommitUploadStage(second.ID); err != nil || !dup {
		t.Fatal(dup, err)
	}
	w = receiptCall(a, u, second.ID)
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Track.ID != track.ID || !result.Duplicate {
		t.Fatal(w.Code, w.Body)
	}
	// Reading a receipt must not reapply edits or generate change revisions.
	revision := a.store.Revision()
	receiptCall(a, u, second.ID)
	if revision != a.store.Revision() {
		t.Fatal("receipt mutated shared state")
	}
	a.store.db.Exec("UPDATE meta SET value=json_set(value,'$.created_at','2000-01-01T00:00:00Z') WHERE key=?", "upload_receipt/"+first.ID)
	if w := receiptCall(a, u, first.ID); w.Code != 404 {
		t.Fatal("expired", w.Code)
	}
	if err := a.store.CleanupResumableUploads(); err != nil {
		t.Fatal(err)
	}
	var count int
	a.store.db.QueryRow("SELECT count(*) FROM meta WHERE key=?", "upload_receipt/"+first.ID).Scan(&count)
	if count != 0 {
		t.Fatal("expired receipt retained")
	}
	setEmailTestRequirement(t, a.store, u.ID, true)
	if r := sessionRequest(a, freshToken, "GET", "/api/v2/uploads/receipts/"+second.ID, ""); r.Code != 200 {
		t.Fatal("later email requirement hid completed receipt", r.Code, r.Body)
	}
}

func TestUploadReceiptFailureRollsBackCommit(t *testing.T) {
	a, _ := testAPI(t)
	p, err := a.store.StageAudio("rollback.wav", bytes.NewReader(validTestAudio(t)), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.store.db.Exec("CREATE TRIGGER reject_receipt BEFORE INSERT ON meta WHEN NEW.key LIKE 'upload_receipt/%' BEGIN SELECT RAISE(ABORT,'receipt failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.CommitUploadStage(p.ID); err == nil || !strings.Contains(err.Error(), "receipt failure") {
		t.Fatal(err)
	}
	if _, ok, err := a.store.UploadStage(p.ID); err != nil || !ok {
		t.Fatal("failed receipt lost stage", err)
	}
	if len(a.store.Tracks()) != 1 {
		t.Fatal("failed receipt published track")
	}
}
