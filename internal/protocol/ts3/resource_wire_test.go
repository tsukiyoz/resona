package ts3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/tsukiyoz/resona/internal/client"
	"github.com/tsukiyoz/resona/internal/iconcache"
)

// This is a synthetic wire-size comparison, not a desktop memory benchmark.
func TestSyntheticWorkspaceResourceWireSize(t *testing.T) {
	store := iconcache.New("")
	dataURLs := make([]string, 21)
	refs := make([]string, 21)
	downloads := 0
	for i := range dataURLs {
		icon := image.NewNRGBA(image.Rect(0, 0, 64, 64))
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				icon.SetNRGBA(x, y, color.NRGBA{R: byte(x*3 + i*17), G: byte(y*4 + i*11), B: byte((x*y + i*7) % 256), A: 255})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, icon); err != nil {
			t.Fatal(err)
		}
		data, err := rasterIconDataURL(encoded.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		dataURLs[i] = data
		key := iconcache.Key{Protocol: "ts3", ServerUID: "synthetic-server", Address: "fixture.invalid:9987", Endpoint: "127.0.0.1:9987", IdentityUID: "synthetic-user", IconID: fmt.Sprint(1000 + i), TransformVersion: iconTransformVersion}
		refs[i], err = store.Reference(context.Background(), key, func(context.Context) (string, error) { downloads++; return data, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	workspace := client.Workspace{Session: client.Session{ID: "synthetic-session", Mode: "connected"}, Channels: []client.Channel{}, Users: []client.User{}, Messages: []client.Message{}, Notifications: []client.Notification{}}
	for i := 0; i < 22; i++ {
		workspace.Channels = append(workspace.Channels, client.Channel{ID: fmt.Sprint(i + 1), Name: fmt.Sprintf("Synthetic channel %02d", i+1), Kind: "channel", IconID: fmt.Sprint(1000 + i%21), IconRef: refs[i%21]})
	}
	current, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(current, &legacy); err != nil {
		t.Fatal(err)
	}
	for i, raw := range legacy["channels"].([]any) {
		channel := raw.(map[string]any)
		delete(channel, "iconRef")
		channel["iconDataURL"] = dataURLs[i%21]
	}
	previous, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(current, []byte("base64")) || bytes.Contains(current, []byte("synthetic-user")) || bytes.Contains(current, []byte("fixture.invalid")) {
		t.Fatal("snapshot leaked icon body or private cache-key input")
	}
	for range 5 {
		for i, ref := range refs {
			data, err := store.Read(context.Background(), ref)
			if err != nil || data != dataURLs[i] {
				t.Fatalf("resource reread: %v", err)
			}
		}
	}
	if downloads != 21 {
		t.Fatalf("repeated resource reads downloaded again: %d", downloads)
	}
	if len(current) >= len(previous) {
		t.Fatal("resource separation did not reduce wire bytes")
	}
	if strings.Contains(string(current), "iconDataURL") {
		t.Fatal("legacy body field remains")
	}
	t.Logf("synthetic 22 channels / 21 unique 64x64 PNGs: legacy=%d bytes, refs=%d bytes, saved=%d bytes (%.1f%%); 105 explicit rereads, total downloads=%d", len(previous), len(current), len(previous)-len(current), 100*(1-float64(len(current))/float64(len(previous))), downloads)
}
