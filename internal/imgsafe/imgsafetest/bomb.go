// Package imgsafetest provides test inputs for code that decodes untrusted
// images. It is imported by tests only.
package imgsafetest

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
)

// DimensionBombPNG returns the UR-11 proof of concept: a 72-byte, well-formed
// PNG whose IHDR declares a 65535×65535 RGBA16 image. png.Decode tries to
// allocate ~34 GB for it (a fatal out-of-memory), while png.DecodeConfig reads
// the dimensions without allocating.
func DimensionBombPNG() []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 65535)
	binary.BigEndian.PutUint32(ihdr[4:], 65535)
	ihdr[8], ihdr[9] = 16, 6 // 16-bit RGBA
	chunk(&b, "IHDR", ihdr)
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(make([]byte, 64))
	w.Close()
	chunk(&b, "IDAT", z.Bytes())
	chunk(&b, "IEND", nil)
	return b.Bytes()
}

func chunk(buf *bytes.Buffer, typ string, data []byte) {
	binary.Write(buf, binary.BigEndian, uint32(len(data)))
	buf.WriteString(typ)
	buf.Write(data)
	c := crc32.NewIEEE()
	c.Write([]byte(typ))
	c.Write(data)
	binary.Write(buf, binary.BigEndian, c.Sum32())
}
