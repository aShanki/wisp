package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"wisp/daemon/internal/api"
)

func sticker(chat, id string, ts int64, file, enc byte, mime string) *api.Message {
	return &api.Message{ID: id, Chat: chat, FromMe: true, Timestamp: ts, Type: api.MsgSticker,
		Media: &api.MediaInfo{Mime: mime, Width: 512, Height: 512},
		MediaRef: &api.MediaRef{DirectPath: "/v/t62/" + id, MediaKey: []byte{1}, Mime: mime,
			FileSHA256: bytes.Repeat([]byte{file}, 32), FileEncSHA256: bytes.Repeat([]byte{enc}, 32)}}
}

// Favourites come first, newest first; then one entry per file we sent, most
// recently sent first; received, Lottie and favourite stickers are left out.
func TestStickers(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	put(t, s, sticker(group, "a", 10, 1, 0xa1, "image/webp"), false)
	put(t, s, sticker(alice, "b", 30, 1, 0xa2, "image/webp"), false) // same file, re-uploaded
	put(t, s, sticker(group, "c", 20, 2, 0xc0, "image/webp"), false)
	put(t, s, sticker(group, "d", 40, 3, 0xd0, "application/was"), false)
	put(t, s, sticker(group, "e", 50, 4, 0xe0, "image/webp"), false)
	put(t, s, txt(group, "t", 60, "hi"), false)
	got := sticker(group, "r", 70, 5, 0x50, "image/webp")
	got.FromMe, got.Sender = false, bob
	put(t, s, got, false)
	id := func(b byte) string { return hex.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

	fav := func(enc byte, ts int64, on bool) {
		t.Helper()
		f := api.EvFavoriteSticker{Favorite: on, TS: ts, Width: 256, Height: 256, Ref: api.MediaRef{
			Mime: "image/webp", DirectPath: "/v/fav", MediaKey: []byte{9}, FileEncSHA256: bytes.Repeat([]byte{enc}, 32)}}
		if err := s.PutFavoriteSticker(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	fav(0xf1, 100, true)
	fav(0xf2, 200, true)
	fav(0xe0, 300, true) // also a recent one
	fav(0xf1, 400, false)

	list, err := s.ListStickers(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.Sticker{
		{ID: id(0xe0), Favorite: true, Width: 256, Height: 256},
		{ID: id(0xf2), Favorite: true, Width: 256, Height: 256},
		{ID: id(0xa2), Width: 512, Height: 512},
		{ID: id(0xc0), Width: 512, Height: 512},
	}
	if len(list) != len(want) {
		t.Fatalf("got %+v", list)
	}
	for i := range want {
		if list[i] != want[i] {
			t.Fatalf("sticker %d: got %+v, want %+v", i, list[i], want[i])
		}
	}
	if got, _ := s.ListStickers(ctx, 1); len(got) != 3 {
		t.Fatalf("recent limit: got %d stickers", len(got))
	}

	ref, st, err := s.StickerRef(ctx, id(0xa2))
	if err != nil || ref.Chat != alice || ref.ID != "b" || ref.DirectPath != "/v/t62/b" || st.Favorite {
		t.Fatalf("message sticker: %+v %+v %v", ref, st, err)
	}
	ref, st, err = s.StickerRef(ctx, id(0xf2))
	if err != nil || ref.Chat != favoriteChat || ref.ID != id(0xf2) || ref.DirectPath != "/v/fav" ||
		ref.Type != api.MsgSticker || !st.Favorite || st.Width != 256 {
		t.Fatalf("favourite: %+v %+v %v", ref, st, err)
	}
	for _, missing := range []string{id(0xf1), "zz", ""} {
		if _, _, err := s.StickerRef(ctx, missing); !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("%q: got %v, want not found", missing, err)
		}
	}
}
