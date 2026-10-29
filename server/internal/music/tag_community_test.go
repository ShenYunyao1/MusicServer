package music

import (
	"errors"
	"testing"
)

func TestCommunityPublicTagCreationAndGovernance(t *testing.T) {
	a, admin, _, member, _, _ := tagLinkFixture(t)
	s := a.store
	tag, err := s.CreateTag(member, TagInput{Scope: "public", GroupID: "language", Name: "日语"})
	if err != nil {
		t.Fatal("member public creation", err)
	}
	if _, err = s.CreateTag(member, TagInput{Scope: "public", GroupID: "language", Name: "日文", Aliases: []string{"Japanese"}}); !errors.Is(err, errTagForbidden) {
		t.Fatal("member created aliases", err)
	}
	name := "Japanese"
	if _, err = s.UpdateTag(member, tag.ID, TagPatch{Name: &name}, tag.Version); !errors.Is(err, errTagForbidden) {
		t.Fatal("member renamed public term", err)
	}
	if err = s.DeleteTag(member, tag.ID, tag.Version); !errors.Is(err, errTagForbidden) {
		t.Fatal("member deleted public term", err)
	}
	if _, err = s.MergeTags(member, tag.ID, tag.ID, tag.Version, tag.Version); !errors.Is(err, errTagForbidden) {
		t.Fatal("member merged public term", err)
	}
	if _, err = s.CreateTagGroup(member, TagGroupInput{Name: "New"}); !errors.Is(err, errTagForbidden) {
		t.Fatal("member created public group", err)
	}
	aliases := []string{"日文"}
	if _, err = s.UpdateTag(admin, tag.ID, TagPatch{Aliases: &aliases}, tag.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTag(member, TagInput{Scope: "public", GroupID: "language", Name: "日文"}); !errors.Is(err, errTagNameTaken) {
		t.Fatal("duplicate alias accepted", err)
	}
}

func TestCommunityMembersTagUnownedTracksAndBatch(t *testing.T) {
	a, admin, _, member, _, _ := tagLinkFixture(t)
	s := a.store
	track := s.Tracks()[0]
	tag := mustLinkTag(t, s, admin, "public", "Shared")
	view, err := s.ChangeTrackTags(member, track.ID, "public", 1, []string{tag.ID}, nil)
	if err != nil || len(view.Public) != 1 {
		t.Fatal("member add to unowned track", view, err)
	}
	job, err := s.PreviewTagBatch(member, TagBatchInput{Scope: "public", TrackIDs: []string{track.ID}, RemoveIDs: []string{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	job, err = s.StartTagBatch(member, job.ID, job.Version)
	if err != nil || job.State != "completed" {
		t.Fatal(job, err)
	}
	view, err = s.GetTrackTags(member, track.ID)
	if err != nil || len(view.Public) != 0 {
		t.Fatal(view, err)
	}
	if got := s.Tracks()[0]; got.OwnerID != track.OwnerID || got.UploadedBy != track.UploadedBy {
		t.Fatal("tagging changed ownership")
	}
}

func TestTagSearchRanksExactAliasPrefixAndContains(t *testing.T) {
	s, auth := tagTestStore(t)
	for _, input := range []TagInput{
		{GroupID: "language", Name: "A contains zz", Aliases: []string{"other"}},
		{GroupID: "language", Name: "zz prefix"},
		{GroupID: "language", Name: "zz"},
		{GroupID: "language", Name: "Alias canonical", Aliases: []string{"zz"}},
	} {
		// Exact names and aliases share a namespace within a group. Use another
		// group for the exact alias so cross-group search still covers both ranks.
		if input.Name == "Alias canonical" {
			input.GroupID = "custom"
		}
		if _, err := s.CreateTag(auth, input); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListTags(auth, TagQuery{Scope: "public", Query: "ZZ", Limit: 2})
	if err != nil || page.Total != 4 || len(page.Items) != 2 || page.Items[0].Name != "zz" || page.Items[1].Name != "Alias canonical" {
		t.Fatal(page, err)
	}
	page, err = s.ListTags(auth, TagQuery{Scope: "public", Query: "ZZ", Offset: 2, Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Items[0].Name != "zz prefix" || page.Items[1].Name != "A contains zz" {
		t.Fatal(page, err)
	}
}
