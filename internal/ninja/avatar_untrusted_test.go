package ninja

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"narutotimer/internal/imgsafe/imgsafetest"
)

// UR-11: an external avatar catalog is read from disk and its index is no
// trust anchor; a dimension bomb with a matching digest must still be rejected
// from its header.
func TestLoadAvatarEntryRejectsDimensionBomb(t *testing.T) {
	bomb := imgsafetest.DimensionBombPNG()
	sum := sha256.Sum256(bomb)
	_, _, err := loadAvatarEntry(avatarIndexEntry{ID: "bomb"}, hex.EncodeToString(sum[:]), func(string) ([]byte, error) { return bomb, nil })
	if err == nil {
		t.Fatal("bomb avatar accepted")
	}
}
