package music

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Compare actual HTTP projections with the same schemas consumed by clients.
// The tags CI job provides Node and enables this check explicitly.
func TestProtocolContractLiveResponse(t *testing.T) {
	if os.Getenv("MUSIC_SERVER_CONTRACT_LIVE") != "1" {
		t.Skip("enabled by contract browser CI")
	}
	a, owner, reader, p := communityFixture(t)
	if _, err := a.store.SetPlaylistVisibility(p.ID, "public", MutationOptions{UserID: owner.ID, ExpectedVersion: p.Version}); err != nil {
		t.Fatal(err)
	}
	token := communityLogin(t, a.store, reader.Username)
	program := `import fs from 'node:fs'; import path from 'node:path'; import {pathToFileURL} from 'node:url';
const {loadSchema,validate}=await import(pathToFileURL(path.resolve('contracts/lib/validate.mjs')));
const file=path.resolve('contracts/schemas/realtime-sync-response.schema.json');
const errors=validate(JSON.parse(fs.readFileSync(0,'utf8')),loadSchema(file),{file});
if(errors.length){console.error(errors.join('\n'));process.exit(1)}`
	for _, state := range []string{"active", "unavailable"} {
		if state == "unavailable" {
			if err := a.store.transaction(func(tx *sql.Tx) error {
				track, err := readTrackFrom(tx, p.TrackIDs[0], true)
				if err != nil {
					return err
				}
				track.Removed = true
				return writeTrack(tx, track)
			}); err != nil {
				t.Fatal(err)
			}
		}
		body, _ := json.Marshal(map[string]any{"view": map[string]any{"catalog": true, "public_detail": p.ID}})
		w := mailRequest(a, token, "POST", "/api/v2/community/sync", string(body), 0)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		cmd := exec.Command("node", "--input-type=module", "-e", program)
		cmd.Dir = filepath.Join("..", "..", "..")
		cmd.Stdin = bytes.NewReader(w.Body.Bytes())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s wire contract: %v\n%s", state, err, output)
		}
	}
}

// TestProtocolContractExamples keeps contracts/examples structurally tied to
// the real wire types. It is intentionally not a golden-file test: it decodes
// every example with DisallowUnknownFields so a renamed or removed JSON tag
// fails before the protocol document silently drifts.
func TestProtocolContractExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "contracts", "examples")
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no contract examples under %s", dir)
	}
	for _, file := range files {
		name := filepath.Base(file)
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			switch {
			case strings.HasPrefix(name, "sync-tracks-request"):
				var value struct {
					Cursor string   `json:"cursor"`
					IDs    []string `json:"ids"`
				}
				decodeContractStrict(t, raw, &value)
				if value.Cursor == "" || len(value.IDs) == 0 {
					t.Fatalf("empty batch example: %+v", value)
				}
			case strings.HasPrefix(name, "sync-tracks-response"):
				var value SyncTracksResponse
				decodeContractStrict(t, raw, &value)
				if len(value.Tracks) == 0 {
					t.Fatal("empty resolved track list")
				}
			case strings.HasPrefix(name, "sync-"):
				var value SyncResponse
				decodeContractStrict(t, raw, &value)
				if value.Cursor == "" {
					t.Fatal("library sync example has no cursor")
				}
			case strings.HasPrefix(name, "realtime-sync-request"):
				var value struct {
					Cursor string       `json:"cursor"`
					View   realtimeView `json:"view"`
				}
				decodeContractStrict(t, raw, &value)
				if !value.View.valid() {
					t.Fatal("realtime request example has an invalid view")
				}
			case strings.HasPrefix(name, "realtime-view"):
				var value realtimeView
				decodeContractStrict(t, raw, &value)
				if !value.valid() {
					t.Fatal("realtime view example is not accepted by the server")
				}
			case strings.HasPrefix(name, "realtime-"):
				var value realtimeReply
				decodeContractStrict(t, raw, &value)
				if value.Cursor == "" {
					t.Fatal("realtime response example has no cursor")
				}
				checkRealtimeExampleSections(t, value.Sections)
			case strings.HasPrefix(name, "error-"):
				var value struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				decodeContractStrict(t, raw, &value)
				if value.Error.Code == "" || value.Error.Message == "" {
					t.Fatal("incomplete error envelope")
				}
			default:
				t.Fatalf("example %s has no Go contract check", name)
			}
		})
	}
}

func decodeContractStrict(t *testing.T, raw []byte, value any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("example contains trailing data: %v", err)
	}
}

func checkRealtimeExampleSections(t *testing.T, sections map[string]realtimeDelta) {
	t.Helper()
	for name, delta := range sections {
		switch name {
		case "catalog":
			var value []PublicPlaylistSummary
			decodeContractStrict(t, mustJSON(t, delta.Changed), &value)
		case "subscriptions", "owner_playlist", "detail_playlist":
			var value []struct {
				Playlist
				TrackCount      int  `json:"track_count"`
				MembersComplete bool `json:"members_complete"`
			}
			decodeContractStrict(t, mustJSON(t, delta.Changed), &value)
		case "invitations", "owner_invitations":
			var value []PlaylistInvitation
			decodeContractStrict(t, mustJSON(t, delta.Changed), &value)
		case "subscription_tracks", "detail_tracks":
			var value []struct {
				Track
				DetailLoaded bool `json:"detail_loaded"`
			}
			decodeContractStrict(t, mustJSON(t, delta.Changed), &value)
		case "badge":
			if len(delta.Changed) != 0 {
				t.Fatalf("badge section carries rows: %s", delta.Changed)
			}
		default:
			t.Fatalf("unknown realtime section %q", name)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProtocolContractSSEExamples(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "examples", "sse-session.txt"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"ready": true, "change": true, "expired": true, "reconnect": true}
	seen := map[string]bool{}
	for _, block := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		if strings.HasPrefix(block, ":") {
			if !strings.Contains(block, "heartbeat") {
				t.Fatalf("unknown comment frame %q", block)
			}
			continue
		}
		var event string
		var data string
		for _, line := range strings.Split(block, "\n") {
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if !allowed[event] {
			t.Fatalf("unknown SSE event %q", event)
		}
		if data != "{}" {
			t.Fatalf("SSE event %q data = %q, want {}", event, data)
		}
		seen[event] = true
	}
	for event := range allowed {
		if !seen[event] {
			t.Fatalf("SSE example missing %q", event)
		}
	}
}
