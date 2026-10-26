package music

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestStageCoverUpdatePreservesAudioAndEnforcesOwnership(t *testing.T) {
	api, _ := testAPI(t)
	user, session, _ := loginTestAccount(t, api.store, "admin")
	options := MutationOptions{UserID: user.ID, ActorType: "session", Actor: user.Username, CredentialID: session.ID}
	preview, err := api.store.StageAudio("attachment.wav", bytes.NewReader(validTestAudio(t)), nil, options)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := api.store.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", preview.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	if err := png.Encode(&data, picture); err != nil {
		t.Fatal(err)
	}
	updated, err := api.store.updateStageCover(preview.ID, data.Bytes(), "external", options)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != preview.ID || updated.ContentHash != preview.ContentHash || updated.Track.CoverID == "" {
		t.Fatal("cover update replaced the staged audio")
	}
	var after string
	_ = api.store.db.QueryRow("SELECT temp_path FROM upload_staging WHERE id=?", preview.ID).Scan(&after)
	if before != after {
		t.Fatal("audio was copied again")
	}
	if _, err := api.store.updateStageCover(preview.ID, data.Bytes(), "external", MutationOptions{UserID: "another-user"}); err == nil {
		t.Fatal("another user changed the staged cover")
	}
	if _, err := api.store.updateStageCover(preview.ID, []byte("not an image"), "manual", options); err == nil {
		t.Fatal("invalid sidecar accepted")
	}
}
