package scene

import (
	"os"
	"path/filepath"
	"testing"

	"narutotimer/internal/imgsafe/imgsafetest"
)

// UR-11: templates of a custom manifest come from disk; a dimension bomb must
// be rejected from its header instead of exhausting memory.
func TestLoadGrayRejectsDimensionBomb(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomb.png")
	if err := os.WriteFile(path, imgsafetest.DimensionBombPNG(), 0o600); err != nil {
		t.Fatal(err)
	}
	if img, err := loadGray(path); err == nil || img != nil {
		t.Fatalf("bomb template accepted: %v", err)
	}
}
