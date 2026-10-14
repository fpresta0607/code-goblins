package cirunner

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ways a test image keeps a cluster.
const (
	keptAsData       = "data"
	keptCompressed   = "compressed"
	keptAsZero       = "marked zero"
	keptUnwritten    = "unwritten"
	keptWithoutTable = "without a table"
)

// qcow2Builder makes a qcow2 version 3 image of a disk, cluster by cluster,
// as QEMU's docs/interop/qcow2.txt lays one out.
type qcow2Builder struct {
	clusterBits uint32
	disk        []byte
	kinds       []string
	header      []byte
	// size, when set, is the disk's size in the header, short of its last
	// cluster's end.
	size int
}

// build returns the image's bytes. The header takes cluster 0, the L1
// table cluster 1, and each L2 table and data cluster those after, in order.
// A compressed cluster starts 100 bytes into a cluster of its own, so its
// offset is no sector's start.
func (b qcow2Builder) build(t *testing.T) []byte {
	t.Helper()
	clusterSize := 1 << b.clusterBits
	l2Entries := clusterSize / 8
	clusters := len(b.disk) / clusterSize
	l1Size := (clusters + l2Entries - 1) / l2Entries
	image := make([]byte, 2*clusterSize)
	header := image[:112]
	copy(header, "QFI\xfb")
	be := binary.BigEndian
	be.PutUint32(header[4:8], 3)
	be.PutUint32(header[20:24], b.clusterBits)
	size := len(b.disk)
	if b.size != 0 {
		size = b.size
	}
	be.PutUint64(header[24:32], uint64(size))
	be.PutUint32(header[36:40], uint32(l1Size))
	be.PutUint64(header[40:48], uint64(clusterSize))
	be.PutUint32(header[96:100], 4)
	be.PutUint32(header[100:104], 112)
	copy(header, b.header[:min(len(b.header), 112)])
	appendCluster := func(data []byte) uint64 {
		at := uint64(len(image))
		image = append(image, make([]byte, clusterSize)...)
		copy(image[at:], data)
		return at
	}
	for table := range l1Size {
		first := table * l2Entries
		if b.kinds[first] == keptWithoutTable {
			continue
		}
		l2At := appendCluster(nil)
		be.PutUint64(image[clusterSize+table*8:], l2At|1<<63)
		for index := range min(l2Entries, clusters-first) {
			cluster := first + index
			data := b.disk[cluster*clusterSize : (cluster+1)*clusterSize]
			var entry uint64
			switch b.kinds[cluster] {
			case keptAsData:
				entry = appendCluster(data) | 1<<63
			case keptCompressed:
				var compressed bytes.Buffer
				writer, err := flate.NewWriter(&compressed, flate.BestCompression)
				if err != nil {
					t.Fatal(err)
				}
				writer.Write(data)
				writer.Close()
				offset := appendCluster(nil) + 100
				for uint64(len(image)) < offset+uint64(compressed.Len()) {
					appendCluster(nil)
				}
				copy(image[offset:], compressed.Bytes())
				offsetBits := 62 - (b.clusterBits - 8)
				extraSectors := (offset%512 + uint64(compressed.Len()) - 1) / 512
				entry = qcow2Compressed | extraSectors<<offsetBits | offset
			case keptAsZero:
				entry = appendCluster(bytes.Repeat([]byte{0xee}, clusterSize)) | qcow2ReadsAsZero | 1<<63
			}
			be.PutUint64(image[l2At+uint64(index)*8:], entry)
		}
	}
	return image
}

// testDisk is clusters of 2^clusterBits bytes, kept as kinds says: data and
// compressed clusters random, so a compressed one takes more sectors than
// its first, and the rest zeros.
func testDisk(clusterBits uint32, kinds []string) qcow2Builder {
	clusterSize := 1 << clusterBits
	random := rand.New(rand.NewPCG(1, 2))
	disk := make([]byte, len(kinds)*clusterSize)
	for cluster, kind := range kinds {
		data := disk[cluster*clusterSize : (cluster+1)*clusterSize]
		if kind == keptAsData || kind == keptCompressed {
			for i := range data {
				data[i] = byte(random.Uint32())
			}
		}
	}
	return qcow2Builder{clusterBits: clusterBits, disk: disk, kinds: kinds}
}

// convert writes image to a file, converts it, and returns the VHD's bytes.
func convert(t *testing.T, image []byte) ([]byte, error) {
	t.Helper()
	dir := t.TempDir()
	source, target := filepath.Join(dir, "disk.img"), filepath.Join(dir, "disk.vhd")
	if err := os.WriteFile(source, image, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertQCOW2(source, target, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)); err != nil {
		return nil, err
	}
	return os.ReadFile(target)
}

// Every way a cluster is kept converts to the disk's bytes, at either end of
// qcow2's cluster sizes and at the size Canonical's images use, and the disk
// ends with a fixed VHD's footer.
func TestConvertQCOW2WritesTheDiskItHolds(t *testing.T) {
	for _, clusterBits := range []uint32{9, 12, 16} {
		t.Run(fmt.Sprintf("clusters of 2^%d bytes", clusterBits), func(t *testing.T) {
			// Arrange
			l2Entries := (1 << clusterBits) / 8
			kinds := []string{keptAsData, keptCompressed, keptAsZero, keptUnwritten, keptCompressed, keptAsData}
			for len(kinds) < l2Entries {
				kinds = append(kinds, keptUnwritten)
			}
			for range l2Entries {
				kinds = append(kinds, keptWithoutTable)
			}
			kinds = append(kinds, keptCompressed, keptAsData)
			disk := testDisk(clusterBits, kinds)

			// Act
			vhd, err := convert(t, disk.build(t))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(vhd) != len(disk.disk)+512 {
				t.Fatalf("the VHD is %d bytes; want the %d-byte disk and a 512-byte footer", len(vhd), len(disk.disk))
			}
			clusterSize := 1 << clusterBits
			for cluster, kind := range kinds {
				at := cluster * clusterSize
				if !bytes.Equal(vhd[at:at+clusterSize], disk.disk[at:at+clusterSize]) {
					t.Errorf("cluster %d, kept %s, converted wrong", cluster, kind)
				}
			}
			footer := vhd[len(disk.disk):]
			if string(footer[:8]) != "conectix" || binary.BigEndian.Uint64(footer[48:56]) != uint64(len(disk.disk)) || binary.BigEndian.Uint32(footer[60:64]) != 2 {
				t.Errorf("footer % x; want a fixed VHD's footer for %d bytes", footer[:64], len(disk.disk))
			}
		})
	}
}

// An image the converter cannot read whole is refused, never read in part.
func TestConvertQCOW2RefusesWhatItCannotReadWhole(t *testing.T) {
	for name, row := range map[string]struct {
		header func([]byte)
		want   string
	}{
		"not qcow2":                  {header: func(h []byte) { copy(h, "QFI\x00") }, want: "not qcow2"},
		"version 4":                  {header: func(h []byte) { binary.BigEndian.PutUint32(h[4:8], 4) }, want: "version 4"},
		"a backing file":             {header: func(h []byte) { binary.BigEndian.PutUint64(h[8:16], 512) }, want: "backing file"},
		"encrypted":                  {header: func(h []byte) { binary.BigEndian.PutUint32(h[32:36], 2) }, want: "encrypted"},
		"clusters too big":           {header: func(h []byte) { binary.BigEndian.PutUint32(h[20:24], 22) }, want: "outside qcow2's"},
		"a part of a sector":         {header: func(h []byte) { binary.BigEndian.PutUint64(h[24:32], 1000) }, want: "whole number of sectors"},
		"dirty":                      {header: func(h []byte) { binary.BigEndian.PutUint64(h[72:80], qcow2Dirty) }, want: "dirty or corrupt"},
		"corrupt":                    {header: func(h []byte) { binary.BigEndian.PutUint64(h[72:80], qcow2Corrupt) }, want: "dirty or corrupt"},
		"an external data file":      {header: func(h []byte) { binary.BigEndian.PutUint64(h[72:80], qcow2ExternalData) }, want: "another file"},
		"extended L2 entries":        {header: func(h []byte) { binary.BigEndian.PutUint64(h[72:80], qcow2ExtendedL2) }, want: "extended L2"},
		"a feature it does not know": {header: func(h []byte) { binary.BigEndian.PutUint64(h[72:80], 1<<5) }, want: "does not know"},
		"zstd": {header: func(h []byte) {
			binary.BigEndian.PutUint64(h[72:80], qcow2CompressionType)
			h[104] = 1
		}, want: "compression type 1"},
		"an L1 table past the end": {header: func(h []byte) { binary.BigEndian.PutUint64(h[40:48], 1<<40) }, want: "runs past the image's end"},
		"an L1 table too short":    {header: func(h []byte) { binary.BigEndian.PutUint32(h[36:40], 0) }, want: "L1 table ends before cluster"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			disk := testDisk(9, []string{keptAsData, keptCompressed})
			image := disk.build(t)
			row.header(image)

			// Act
			_, err := convert(t, image)

			// Assert
			if err == nil || !strings.Contains(err.Error(), row.want) {
				t.Errorf("ConvertQCOW2 = %v; want it refused as %q", err, row.want)
			}
		})
	}
}

// A cluster whose entry points past the image's end, or whose compressed data
// does not inflate to a cluster, is an error naming the cluster.
func TestConvertQCOW2RefusesAClusterItCannotRead(t *testing.T) {
	for name, entry := range map[string]func(image []byte, l2At int) uint64{
		"data past the end":       func(image []byte, _ int) uint64 { return uint64(len(image)) + 512 },
		"compressed past the end": func(image []byte, _ int) uint64 { return qcow2Compressed | uint64(len(image)) + 512 },
		"compressed garbage":      func(_ []byte, l2At int) uint64 { return qcow2Compressed | uint64(l2At) },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			disk := testDisk(9, []string{keptAsData, keptAsData})
			image := disk.build(t)
			l2At := int(binary.BigEndian.Uint64(image[512:]) & qcow2OffsetMask)
			binary.BigEndian.PutUint64(image[l2At+8:], entry(image, l2At))

			// Act
			_, err := convert(t, image)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "the image's cluster 1") {
				t.Errorf("ConvertQCOW2 = %v; want cluster 1 refused", err)
			}
		})
	}
}

// The footer is a fixed VHD's, its checksum the ones' complement of its
// other bytes' sum, and its geometry the specification's for the disk's size.
func TestVHDFooterDescribesAFixedDisk(t *testing.T) {
	for _, row := range []struct {
		size                   uint64
		cylinders              uint16
		heads, sectorsPerTrack uint8
	}{
		{size: 1 << 20, cylinders: 30, heads: 4, sectorsPerTrack: 17},
		{size: 3_758_096_384, cylinders: 7281, heads: 16, sectorsPerTrack: 63},
		{size: 64 << 30, cylinders: 32896, heads: 16, sectorsPerTrack: 255},
	} {
		t.Run(fmt.Sprint(row.size), func(t *testing.T) {
			// Act
			footer, err := vhdFooter(row.size, time.Date(2000, 1, 1, 0, 1, 0, 0, time.UTC))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			be := binary.BigEndian
			var sum uint32
			for i, b := range footer {
				if i < 64 || i >= 68 {
					sum += uint32(b)
				}
			}
			switch {
			case len(footer) != 512 || string(footer[:8]) != "conectix" || be.Uint32(footer[8:12]) != 2 || be.Uint32(footer[12:16]) != 0x00010000:
				t.Errorf("footer starts % x; want a version 1.0 VHD footer", footer[:16])
			case be.Uint64(footer[16:24]) != ^uint64(0) || be.Uint32(footer[60:64]) != 2:
				t.Errorf("data offset %#x, disk type %d; want a fixed disk", be.Uint64(footer[16:24]), be.Uint32(footer[60:64]))
			case be.Uint32(footer[24:28]) != 60:
				t.Errorf("time stamp %d; want 60 seconds past 2000", be.Uint32(footer[24:28]))
			case be.Uint64(footer[40:48]) != row.size || be.Uint64(footer[48:56]) != row.size:
				t.Errorf("sizes %d and %d; want %d", be.Uint64(footer[40:48]), be.Uint64(footer[48:56]), row.size)
			case be.Uint16(footer[56:58]) != row.cylinders || footer[58] != row.heads || footer[59] != row.sectorsPerTrack:
				t.Errorf("geometry %d/%d/%d; want %d/%d/%d", be.Uint16(footer[56:58]), footer[58], footer[59], row.cylinders, row.heads, row.sectorsPerTrack)
			case be.Uint32(footer[64:68]) != ^sum:
				t.Errorf("checksum %#x; want %#x", be.Uint32(footer[64:68]), ^sum)
			case bytes.Equal(footer[68:84], make([]byte, 16)):
				t.Error("the footer's unique id is all zeros")
			}
		})
	}
}

// A disk whose size ends part way into its last cluster ends there, and its
// footer follows.
func TestConvertQCOW2EndsTheDiskAtItsSize(t *testing.T) {
	// Arrange
	disk := testDisk(12, []string{keptAsData, keptCompressed, keptAsData})
	disk.size = len(disk.disk) - 1024

	// Act
	vhd, err := convert(t, disk.build(t))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(vhd) != disk.size+512 || !bytes.Equal(vhd[:disk.size], disk.disk[:disk.size]) || string(vhd[disk.size:disk.size+8]) != "conectix" {
		t.Errorf("the VHD is %d bytes; want the %d-byte disk, then its footer", len(vhd), disk.size)
	}
}

// A compressed cluster whose data inflates to less than a cluster is an
// error, never a cluster ending in zeros.
func TestConvertQCOW2RefusesACompressedClusterThatIsShort(t *testing.T) {
	// Arrange
	disk := testDisk(9, []string{keptAsData, keptAsData})
	image := disk.build(t)
	l2At := binary.BigEndian.Uint64(image[512:]) & qcow2OffsetMask
	at := binary.BigEndian.Uint64(image[l2At+8:]) & qcow2OffsetMask
	var short bytes.Buffer
	writer, err := flate.NewWriter(&short, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	writer.Write(bytes.Repeat([]byte("short"), 20))
	writer.Close()
	copy(image[at:at+512], make([]byte, 512))
	copy(image[at:], short.Bytes())
	binary.BigEndian.PutUint64(image[l2At+8:], qcow2Compressed|at)

	// Act
	_, err = convert(t, image)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "the image's cluster 1: compressed data") || !strings.Contains(err.Error(), "does not inflate to a cluster") {
		t.Errorf("ConvertQCOW2 = %v; want cluster 1 refused as short", err)
	}
}
