package music

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestMultiuserUploadOwnershipAndDedup(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "upload-a")
	b, bt := readyMember(t, api, "upload-b")
	audio := validTestAudio(t)
	pa, err := api.store.StageAudio("a.wav", bytes.NewReader(audio), map[string]string{"title": "A title"}, MutationOptions{UserID: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if pa.Track.OwnerID != a.ID || pa.Track.UploadedBy != a.ID {
		t.Fatal("preview owner missing")
	}
	pb, err := api.store.StageAudio("b.wav", bytes.NewReader(audio), map[string]string{"title": "B title"}, MutationOptions{UserID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := memberVersionRequest(api, bt, "POST", "/api/v2/uploads/stage/"+pa.ID+"/commit", `{}`, 0)
	if r.Code != 404 {
		t.Fatal("foreign stage", r.Code)
	}
	r = memberVersionRequest(api, at, "GET", "/api/v2/uploads/stage/"+pa.ID, "", 0)
	if r.Code != 200 {
		t.Fatal("own preview", r.Code, r.Body.String())
	}
	var wg sync.WaitGroup
	results := make(chan Track, 2)
	errs := make(chan error, 2)
	for _, entry := range []struct{ id, user string }{{pa.ID, a.ID}, {pb.ID, b.ID}} {
		wg.Add(1)
		go func(id, user string) {
			defer wg.Done()
			tr, _, err := api.store.CommitUploadStage(id, MutationOptions{UserID: user})
			results <- tr
			errs <- err
		}(entry.id, entry.user)
	}
	wg.Wait()
	one, two := <-results, <-results
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID || one.OwnerID != two.OwnerID || one.UploadedBy != two.UploadedBy || one.Title != two.Title {
		t.Fatal("dedup changed provenance", one, two)
	}
	if err := api.store.DeleteTrack(one.ID); err != nil {
		t.Fatal(err)
	}
	pc, err := api.store.StageAudio("again.wav", bytes.NewReader(audio), nil, MutationOptions{UserID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = api.store.CommitUploadStage(pc.ID, MutationOptions{UserID: b.ID})
	if err == nil {
		t.Fatal("removed content reuploaded")
	}
	if r := memberVersionRequest(api, bt, http.MethodDelete, "/api/v2/uploads/stage/"+pc.ID, "", 0); r.Code != 204 {
		t.Fatal("cancel", r.Code)
	}
}

func TestMultiuserDirectUploadReportsDuplicate(t *testing.T) {
	api, _ := testAPI(t)
	a, at := readyMember(t, api, "direct-a")
	_, bt := readyMember(t, api, "direct-b")
	audio := validTestAudio(t)
	var original string
	for index, token := range []string{at, bt} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		file, _ := form.CreateFormFile("file", "same.wav")
		file.Write(audio)
		form.Close()
		r := httptest.NewRequest("POST", "http://example.com/api/v2/uploads", &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("X-CSRF-Token", csrfForToken(token))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		var view struct {
			Track
			Duplicate bool `json:"duplicate"`
		}
		json.Unmarshal(w.Body.Bytes(), &view)
		if index == 0 {
			original = view.ID
			if w.Code != 201 || view.Duplicate {
				t.Fatal("new upload", w.Code, w.Body.String())
			}
		} else if w.Code != 200 || !view.Duplicate || view.ID != original || view.OwnerID != a.ID || view.Path != "" {
			t.Fatal("duplicate response", w.Code, w.Body.String())
		}
	}
}
