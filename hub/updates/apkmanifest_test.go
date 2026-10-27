package updates

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func put16(b *bytes.Buffer, x uint16) { _ = binary.Write(b, binary.LittleEndian, x) }
func put32(b *bytes.Buffer, x uint32) { _ = binary.Write(b, binary.LittleEndian, x) }
func fixtureAXML() []byte {
	names := []string{"manifest", "package", "com.onespace.companion", "versionCode", "versionName", "0.6.0"}
	var data bytes.Buffer
	offsets := []uint32{}
	for _, s := range names {
		offsets = append(offsets, uint32(data.Len()))
		data.WriteByte(byte(len(s)))
		data.WriteByte(byte(len(s)))
		data.WriteString(s)
		data.WriteByte(0)
	}
	var pool bytes.Buffer
	put16(&pool, 1)
	put16(&pool, 28)
	put32(&pool, uint32(28+4*len(names)+data.Len()))
	put32(&pool, uint32(len(names)))
	put32(&pool, 0)
	put32(&pool, 0x100)
	put32(&pool, uint32(28+4*len(names)))
	put32(&pool, 0)
	for _, v := range offsets {
		put32(&pool, v)
	}
	pool.Write(data.Bytes())
	var el bytes.Buffer
	put16(&el, 0x102)
	put16(&el, 16)
	put32(&el, 16+20+3*20)
	put32(&el, 0)
	put32(&el, 0xffffffff)
	// ResXMLTree_attrExt: ns,name,attributeStart,attributeSize,attributeCount,idIndex,classIndex,styleIndex
	put32(&el, 0xffffffff)
	put32(&el, 0)
	put16(&el, 20)
	put16(&el, 20)
	put16(&el, 3)
	put16(&el, 0)
	put16(&el, 0)
	put16(&el, 0)
	for _, attr := range []struct {
		name, raw, data uint32
		typ             uint8
	}{{1, 2, 2, 3}, {3, 0xffffffff, 6, 0x10}, {4, 5, 5, 3}} {
		put32(&el, 0xffffffff)
		put32(&el, attr.name)
		put32(&el, attr.raw)
		put16(&el, 8)
		el.WriteByte(0)
		el.WriteByte(attr.typ)
		put32(&el, attr.data)
	}
	var out bytes.Buffer
	put16(&out, 3)
	put16(&out, 8)
	put32(&out, uint32(8+pool.Len()+el.Len()))
	out.Write(pool.Bytes())
	out.Write(el.Bytes())
	return out.Bytes()
}
func fixtureAPK(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, e := z.Create("AndroidManifest.xml")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write(fixtureAXML())
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return buf.Bytes()
}
func TestReadRealisticAPKBinaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.apk")
	if e := os.WriteFile(path, fixtureAPK(t), 0600); e != nil {
		t.Fatal(e)
	}
	x, e := readAPKInfo(path)
	if e != nil {
		t.Fatal(e)
	}
	if x.Package != "com.onespace.companion" || x.VersionCode != 6 || x.VersionName != "0.6.0" {
		t.Fatalf("bad info: %+v", x)
	}
	if _, e := parseAXML([]byte{3, 0}); e == nil {
		t.Fatal("accepted truncated XML")
	}
}
