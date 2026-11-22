package config

import (
	"encoding/base64"
	"testing"
)

func TestDecodeHash(t *testing.T) {
	raw := "$2a$12$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234"
	if decodeHash(raw) != raw {
		t.Fatal("raw hash changed")
	}
	if decodeHash(base64.StdEncoding.EncodeToString([]byte(raw))) != raw {
		t.Fatal("base64 hash not decoded")
	}
	if decodeHash("  ") != "" || decodeHash("not-a-hash") != "not-a-hash" {
		t.Fatal("unexpected decode")
	}
}
