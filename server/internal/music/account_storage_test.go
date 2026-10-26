package music

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"musicserver/internal/diskspace"
)

func TestAccountStorageBoundedAndSelfOnly(t *testing.T) {
	api, _ := testAPI(t)
	member, token := readyMember(t, api, "quota-summary-member")
	admin, _, _ := loginTestAccount(t, api.store, "admin")
	api.store.spaceProbe = func(string) (diskspace.Space, error) {
		t.Fatal("account summary must not probe global disk volumes")
		return diskspace.Space{}, nil
	}
	if _, err := api.store.SetStorageQuota(member.ID, 1024, 0, MutationOptions{UserID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	response := sessionRequest(api, token, "GET", "/api/v2/auth/storage?ids="+admin.ID, "")
	var self accountStorageView
	if err := json.Unmarshal(response.Body.Bytes(), &self); err != nil || response.Code != 200 || self.ID != member.ID || self.LimitBytes != 1024 || self.QuotaSource != "administrator" {
		t.Fatalf("self boundary: %d %s %v", response.Code, response.Body, err)
	}
	if response := sessionRequest(api, token, "GET", "/api/v2/admin/storage/accounts?ids="+admin.ID, ""); response.Code != 403 {
		t.Fatal(response.Code, response.Body)
	}
	response = callAPI(api, "GET", "/api/v2/admin/storage/accounts?ids="+member.ID+","+admin.ID, "")
	var page accountStoragePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || page.Total != 2 || len(page.Items) != 2 {
		t.Fatal(response.Code, response.Body, err)
	}
	for _, query := range []string{"", "ids=" + strings.Repeat("x,", 100) + "x", "ids=" + member.ID + "," + member.ID, "deleted=true&limit=101", "deleted=true&offset=-1", "deleted=true&ids=" + member.ID} {
		if w := callAPI(api, "GET", "/api/v2/admin/storage/accounts?"+query, ""); w.Code != 400 {
			t.Fatalf("unbounded summary %s: %d %s", query, w.Code, w.Body)
		}
	}
	request := httptest.NewRequest("GET", "/api/v2/auth/storage", nil)
	request.Header.Set("X-API-Key", "invalid")
	w := httptest.NewRecorder()
	api.ServeHTTP(w, request)
	if w.Code != 401 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestAccountStorageDeletedHistoryIsPaged(t *testing.T) {
	api, _ := testAPI(t)
	for _, name := range []string{"quota-deleted-a", "quota-deleted-b"} {
		user, _ := readyMember(t, api, name)
		if _, err := api.store.db.Exec("INSERT INTO meta(key,value) VALUES(?,?)", "deleted_user/"+user.ID, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	response := callAPI(api, "GET", "/api/v2/admin/storage/accounts?deleted=true&limit=1&offset=1", "")
	var page accountStoragePage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || response.Code != 200 || page.Total != 2 || len(page.Items) != 1 || page.Offset != 1 || !page.Items[0].Deleted {
		t.Fatal(response.Code, response.Body, err)
	}
}
