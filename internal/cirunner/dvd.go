package cirunner

import (
	"encoding/binary"
	"os"
)

// The job's DVD is the smallest ISO 9660 volume that holds one file: sixteen
// empty sectors, the primary volume descriptor, the descriptor set's
// terminator, the path table in each byte order, the root directory, and the
// file, then 150 empty sectors, as mkisofs pads an image by default, so a
// reader's read-ahead past the file stays inside the volume.
const (
	sectorSize       = 2048
	descriptorSector = 16
	littlePathSector = 18
	bigPathSector    = 19
	rootSector       = 20
	fileSector       = 21
	paddingSectors   = 150
	pathTableSize    = 10
	// DVDLabel is the label the VM finds its DVD by, and DVDFile the one
	// file on it, which Linux lists as jitconfig.
	DVDLabel = "CGJOB"
	DVDFile  = "JITCONFIG.;1"
)

// BuildDVD writes, at path, the DVD the job's VM reads its runner's
// configuration from, with config as its one file.
func BuildDVD(path, config string) error {
	return os.WriteFile(path, dvdImage([]byte(config)), 0o600)
}

func dvdImage(content []byte) []byte {
	fileSectors := (len(content) + sectorSize - 1) / sectorSize
	image := make([]byte, (fileSector+fileSectors+paddingSectors)*sectorSize)

	descriptor := image[descriptorSector*sectorSize:]
	descriptor[0] = 1
	copy(descriptor[1:6], "CD001")
	descriptor[6] = 1
	spaced(descriptor[8:40], "")
	spaced(descriptor[40:72], DVDLabel)
	both32(descriptor[80:88], uint32(len(image)/sectorSize))
	both16(descriptor[120:124], 1)
	both16(descriptor[124:128], 1)
	both16(descriptor[128:132], sectorSize)
	both32(descriptor[132:140], pathTableSize)
	binary.LittleEndian.PutUint32(descriptor[140:144], littlePathSector)
	binary.BigEndian.PutUint32(descriptor[148:152], bigPathSector)
	directoryRecord(descriptor[156:190], rootSector, sectorSize, true, "\x00")
	spaced(descriptor[190:813], "")
	// The four dates, made, changed, expiring and effective, are each given
	// as not specified: sixteen zero digits and no time zone.
	for at := 813; at < 881; at += 17 {
		copy(descriptor[at:at+16], "0000000000000000")
	}
	descriptor[881] = 1

	terminator := image[(descriptorSector+1)*sectorSize:]
	terminator[0] = 255
	copy(terminator[1:6], "CD001")
	terminator[6] = 1

	pathEntry(image[littlePathSector*sectorSize:], binary.LittleEndian)
	pathEntry(image[bigPathSector*sectorSize:], binary.BigEndian)

	root := image[rootSector*sectorSize:]
	at := directoryRecord(root, rootSector, sectorSize, true, "\x00")
	at += directoryRecord(root[at:], rootSector, sectorSize, true, "\x01")
	directoryRecord(root[at:], fileSector, len(content), false, DVDFile)

	copy(image[fileSector*sectorSize:], content)
	return image
}

// directoryRecord writes the record of a file or directory named name, whose
// content is size bytes from sector extent, and returns its length.
func directoryRecord(record []byte, extent, size int, isDirectory bool, name string) int {
	length := 33 + len(name)
	if length%2 == 1 {
		length++
	}
	record[0] = byte(length)
	both32(record[2:10], uint32(extent))
	both32(record[10:18], uint32(size))
	// Recorded on 1 January 1970, at UTC, for an image that is the same
	// whenever it is made.
	copy(record[18:25], []byte{70, 1, 1, 0, 0, 0, 0})
	if isDirectory {
		record[25] = 2
	}
	both16(record[28:32], 1)
	record[32] = byte(len(name))
	copy(record[33:], name)
	return length
}

// pathEntry writes the path table's one entry, the root directory, in order.
func pathEntry(table []byte, order binary.ByteOrder) {
	table[0] = 1
	order.PutUint32(table[2:6], rootSector)
	order.PutUint16(table[6:8], 1)
}

func spaced(field []byte, text string) {
	for i := range field {
		field[i] = ' '
	}
	copy(field, text)
}

func both16(field []byte, value uint16) {
	binary.LittleEndian.PutUint16(field[0:2], value)
	binary.BigEndian.PutUint16(field[2:4], value)
}

func both32(field []byte, value uint32) {
	binary.LittleEndian.PutUint32(field[0:4], value)
	binary.BigEndian.PutUint32(field[4:8], value)
}
