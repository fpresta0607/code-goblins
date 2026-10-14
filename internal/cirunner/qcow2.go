package cirunner

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// The parts of a qcow2 image's entries, from QEMU's docs/interop/qcow2.txt.
const (
	qcow2OffsetMask  = 0x00fffffffffffe00
	qcow2Compressed  = 1 << 62
	qcow2ReadsAsZero = 1
	// qcow2Dirty, qcow2Corrupt, qcow2ExternalData, qcow2CompressionType and
	// qcow2ExtendedL2 are the incompatible features a version 3 image can
	// name. Only the compression type is read, and only as zlib.
	qcow2Dirty             = 1 << 0
	qcow2Corrupt           = 1 << 1
	qcow2ExternalData      = 1 << 2
	qcow2CompressionType   = 1 << 3
	qcow2ExtendedL2        = 1 << 4
	qcow2KnownIncompatible = qcow2Dirty | qcow2Corrupt | qcow2ExternalData | qcow2CompressionType | qcow2ExtendedL2
)

// qcow2Image is what the converter reads of an image's header.
type qcow2Image struct {
	file        *os.File
	fileSize    int64
	clusterBits uint32
	size        uint64
	l1          []uint64
}

// ConvertQCOW2 writes, at target, the disk the qcow2 image at source holds,
// as a fixed VHD, the form Hyper-V reads: the disk's bytes, then its footer.
// An image this converter cannot read whole, one with a backing file,
// encryption, an external data file, extended L2 entries, compression other
// than zlib, or marked dirty or corrupt, is an error, never a disk read in
// part.
func ConvertQCOW2(source, target string, now time.Time) (failure error) {
	image, err := openQCOW2(source)
	if err != nil {
		return err
	}
	defer image.file.Close()
	disk, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, disk.Close()) }()
	clusterSize := uint64(1) << image.clusterBits
	l2Entries := clusterSize / 8
	var l2Offset uint64
	var l2 []uint64
	for cluster := range (image.size + clusterSize - 1) / clusterSize {
		index := cluster / l2Entries
		if index >= uint64(len(image.l1)) {
			return fmt.Errorf("cirunner: the image's L1 table ends before cluster %d", cluster)
		}
		if offset := image.l1[index] & qcow2OffsetMask; offset != l2Offset {
			if l2, err = image.table(offset, l2Entries); err != nil {
				return err
			}
			l2Offset = offset
		}
		var entry uint64
		if l2 != nil {
			entry = l2[cluster%l2Entries]
		}
		data, err := image.cluster(entry, clusterSize)
		if err != nil {
			return fmt.Errorf("cirunner: the image's cluster %d: %w", cluster, err)
		}
		if data == nil {
			continue
		}
		at := cluster * clusterSize
		if _, err := disk.WriteAt(data[:min(clusterSize, image.size-at)], int64(at)); err != nil {
			return err
		}
	}
	// The footer goes at the disk's end, which also sets the file's length
	// past clusters never written.
	footer, err := vhdFooter(image.size, now)
	if err != nil {
		return err
	}
	_, err = disk.WriteAt(footer, int64(image.size))
	return err
}

// openQCOW2 opens the image at path and reads its header and L1 table,
// refusing what the converter cannot read whole.
func openQCOW2(path string) (_ *qcow2Image, failure error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if failure != nil {
			file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	header := make([]byte, 112)
	if _, err := file.ReadAt(header, 0); err != nil {
		return nil, fmt.Errorf("cirunner: read the image's header: %w", err)
	}
	be := binary.BigEndian
	version := be.Uint32(header[4:8])
	image := &qcow2Image{file: file, fileSize: info.Size(), clusterBits: be.Uint32(header[20:24]), size: be.Uint64(header[24:32])}
	var incompatible uint64
	compression := byte(0)
	if version == 3 {
		incompatible = be.Uint64(header[72:80])
		if be.Uint32(header[100:104]) > 104 {
			compression = header[104]
		}
	}
	switch {
	case string(header[0:4]) != "QFI\xfb":
		return nil, errors.New("cirunner: the image is not qcow2")
	case version != 2 && version != 3:
		return nil, fmt.Errorf("cirunner: the image is qcow2 version %d, not 2 or 3", version)
	case be.Uint64(header[8:16]) != 0:
		return nil, errors.New("cirunner: the image has a backing file")
	case be.Uint32(header[32:36]) != 0:
		return nil, errors.New("cirunner: the image is encrypted")
	case image.clusterBits < 9 || image.clusterBits > 21:
		return nil, fmt.Errorf("cirunner: the image's clusters are 2^%d bytes, outside qcow2's 2^9 to 2^21", image.clusterBits)
	case image.size == 0 || image.size%512 != 0:
		return nil, fmt.Errorf("cirunner: the image's disk is %d bytes, not a whole number of sectors", image.size)
	case image.size > vmDiskSize:
		return nil, fmt.Errorf("cirunner: the image's disk is %d bytes, larger than the VM's 64 GiB disk", image.size)
	case incompatible&^qcow2KnownIncompatible != 0:
		return nil, fmt.Errorf("cirunner: the image needs features %#x this converter does not know", incompatible&^qcow2KnownIncompatible)
	case incompatible&(qcow2Dirty|qcow2Corrupt) != 0:
		return nil, errors.New("cirunner: the image is marked dirty or corrupt")
	case incompatible&qcow2ExternalData != 0:
		return nil, errors.New("cirunner: the image keeps its data in another file")
	case incompatible&qcow2ExtendedL2 != 0:
		return nil, errors.New("cirunner: the image uses extended L2 entries")
	case compression != 0:
		return nil, fmt.Errorf("cirunner: the image's clusters use compression type %d, not zlib", compression)
	}
	if image.l1, err = image.table(be.Uint64(header[40:48]), uint64(be.Uint32(header[36:40]))); err != nil {
		return nil, fmt.Errorf("cirunner: the image's L1 table: %w", err)
	}
	return image, nil
}

// table reads entries big-endian entries at offset, as an L1 or L2 table is
// kept. Offset zero is a table never written, which reads as nil.
func (image *qcow2Image) table(offset, entries uint64) ([]uint64, error) {
	if offset == 0 {
		return nil, nil
	}
	if entries > uint64(image.fileSize)/8 || offset+entries*8 > uint64(image.fileSize) {
		return nil, fmt.Errorf("cirunner: a table of %d entries at %d runs past the image's end", entries, offset)
	}
	data := make([]byte, entries*8)
	if _, err := image.file.ReadAt(data, int64(offset)); err != nil {
		return nil, err
	}
	table := make([]uint64, entries)
	for i := range table {
		table[i] = binary.BigEndian.Uint64(data[i*8:])
	}
	return table, nil
}

// cluster reads the cluster an L2 entry names, or nil for one that reads as
// zeros: never written, or marked so.
func (image *qcow2Image) cluster(entry, clusterSize uint64) ([]byte, error) {
	if entry&qcow2Compressed != 0 {
		// The host offset takes the entry's low bits, and the count of
		// 512-byte sectors past the first the next ones, up to bit 62.
		offsetBits := 62 - (image.clusterBits - 8)
		offset := entry & (1<<offsetBits - 1)
		sectors := (entry>>offsetBits)&(1<<(62-offsetBits)-1) + 1
		length := min(sectors*512-offset%512, uint64(image.fileSize)-min(offset, uint64(image.fileSize)))
		if offset >= uint64(image.fileSize) || length == 0 {
			return nil, fmt.Errorf("compressed data at %d is past the image's end", offset)
		}
		compressed := make([]byte, length)
		if _, err := image.file.ReadAt(compressed, int64(offset)); err != nil {
			return nil, err
		}
		data := make([]byte, clusterSize)
		if _, err := io.ReadFull(flate.NewReader(bytes.NewReader(compressed)), data); err != nil {
			return nil, fmt.Errorf("compressed data at %d does not inflate to a cluster: %w", offset, err)
		}
		return data, nil
	}
	offset := entry & qcow2OffsetMask
	if entry&qcow2ReadsAsZero != 0 || offset == 0 {
		return nil, nil
	}
	data := make([]byte, clusterSize)
	if _, err := image.file.ReadAt(data, int64(offset)); err != nil {
		return nil, fmt.Errorf("data at %d: %w", offset, err)
	}
	return data, nil
}
