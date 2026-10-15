package cirunner

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// vhdFooter is the 512 bytes that end a fixed VHD of size bytes, in the
// layout of Microsoft's Virtual Hard Disk Image Format Specification,
// "Hard Disk Footer Format": a fixed disk is its bytes, then this footer.
func vhdFooter(size uint64, now time.Time) ([]byte, error) {
	footer := make([]byte, 512)
	copy(footer[0:8], "conectix")
	binary.BigEndian.PutUint32(footer[8:12], 2)
	binary.BigEndian.PutUint32(footer[12:16], 0x00010000)
	binary.BigEndian.PutUint64(footer[16:24], ^uint64(0))
	binary.BigEndian.PutUint32(footer[24:28], uint32(now.Sub(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))/time.Second))
	copy(footer[28:32], "cgcr")
	binary.BigEndian.PutUint32(footer[32:36], 0x00010000)
	copy(footer[36:40], "Wi2k")
	binary.BigEndian.PutUint64(footer[40:48], size)
	binary.BigEndian.PutUint64(footer[48:56], size)
	cylinders, heads, sectors := vhdGeometry(size / 512)
	binary.BigEndian.PutUint16(footer[56:58], cylinders)
	footer[58], footer[59] = heads, sectors
	binary.BigEndian.PutUint32(footer[60:64], 2)
	if _, err := rand.Read(footer[68:84]); err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint32(footer[64:68], vhdChecksum(footer))
	return footer, nil
}

// vhdChecksum is the ones' complement of the sum of the footer's bytes but
// its own four.
func vhdChecksum(footer []byte) uint32 {
	var sum uint32
	for i, b := range footer {
		if i < 64 || i >= 68 {
			sum += uint32(b)
		}
	}
	return ^sum
}

// vhdGeometry is a disk's cylinders, heads and sectors per track, as the
// specification's appendix computes them from its sectors.
func vhdGeometry(totalSectors uint64) (uint16, uint8, uint8) {
	totalSectors = min(totalSectors, 65535*16*255)
	var sectorsPerTrack, heads, cylinderTimesHeads uint64
	if totalSectors >= 65535*16*63 {
		sectorsPerTrack, heads = 255, 16
		cylinderTimesHeads = totalSectors / sectorsPerTrack
	} else {
		sectorsPerTrack = 17
		cylinderTimesHeads = totalSectors / sectorsPerTrack
		heads = max((cylinderTimesHeads+1023)/1024, 4)
		if cylinderTimesHeads >= heads*1024 || heads > 16 {
			sectorsPerTrack, heads = 31, 16
			cylinderTimesHeads = totalSectors / sectorsPerTrack
		}
		if cylinderTimesHeads >= heads*1024 {
			sectorsPerTrack, heads = 63, 16
			cylinderTimesHeads = totalSectors / sectorsPerTrack
		}
	}
	return uint16(cylinderTimesHeads / heads), uint8(heads), uint8(sectorsPerTrack)
}
