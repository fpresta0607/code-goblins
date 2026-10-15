package cirunner

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readDVD reads an ISO 9660 image as a reader that knows nothing of how
// BuildDVD lays it out would: from the primary volume descriptor's root
// record, through the root directory, to each file it lists.
func readDVD(image []byte) (label string, files map[string][]byte, err error) {
	if len(image) < 17*sectorSize {
		return "", nil, fmt.Errorf("%d bytes is too short for a volume descriptor", len(image))
	}
	descriptor := image[16*sectorSize : 17*sectorSize]
	if descriptor[0] != 1 || string(descriptor[1:6]) != "CD001" || descriptor[6] != 1 {
		return "", nil, fmt.Errorf("sector 16 is no primary volume descriptor: % x", descriptor[:7])
	}
	if size := binary.LittleEndian.Uint32(descriptor[80:84]); int(size)*sectorSize != len(image) || binary.BigEndian.Uint32(descriptor[84:88]) != size {
		return "", nil, fmt.Errorf("the volume says %d sectors; the image has %d bytes", size, len(image))
	}
	terminator := image[17*sectorSize:]
	if terminator[0] != 255 || string(terminator[1:6]) != "CD001" {
		return "", nil, fmt.Errorf("sector 17 is no descriptor set terminator: % x", terminator[:7])
	}
	extent, size := recordExtent(descriptor[156:190])
	if extent < 0 || int(extent+size) > len(image) {
		return "", nil, fmt.Errorf("the root directory's record is out of place: sector %d, %d bytes", extent/sectorSize, size)
	}
	// Each path table lists the root alone, at the root's extent, in its own
	// byte order.
	little, big := image[binary.LittleEndian.Uint32(descriptor[140:144])*sectorSize:], image[binary.BigEndian.Uint32(descriptor[148:152])*sectorSize:]
	if tableSize := binary.LittleEndian.Uint32(descriptor[132:136]); tableSize != 10 || little[0] != 1 || int64(binary.LittleEndian.Uint32(little[2:6]))*sectorSize != extent || big[0] != 1 || int64(binary.BigEndian.Uint32(big[2:6]))*sectorSize != extent {
		return "", nil, fmt.Errorf("the path tables (%d bytes) do not list the root at sector %d: % x and % x", tableSize, extent/sectorSize, little[:10], big[:10])
	}
	files = map[string][]byte{}
	directory := image[extent : extent+size]
	previous := ""
	for at := 0; at < len(directory) && directory[at] != 0; at += int(directory[at]) {
		if directory[at]%2 != 0 {
			return "", nil, fmt.Errorf("a directory record is %d bytes long; ISO 9660 records are even", directory[at])
		}
		record := directory[at : at+int(directory[at])]
		name := string(record[33 : 33+int(record[32])])
		if record[25]&2 != 0 {
			continue
		}
		// ISO 9660 keeps a directory's records in the order of their names.
		if name <= previous {
			return "", nil, fmt.Errorf("the record %q follows %q, out of order", name, previous)
		}
		previous = name
		start, length := recordExtent(record)
		files[name] = image[start : start+length]
	}
	return strings.TrimRight(string(descriptor[40:72]), " "), files, nil
}

// recordExtent is where a directory record's content starts in the image, and
// its length, read from both byte orders, which must agree.
func recordExtent(record []byte) (int64, int64) {
	sector, size := binary.LittleEndian.Uint32(record[2:6]), binary.LittleEndian.Uint32(record[10:14])
	if binary.BigEndian.Uint32(record[6:10]) != sector || binary.BigEndian.Uint32(record[14:18]) != size {
		return -1, -1
	}
	return int64(sector) * sectorSize, int64(size)
}

// The DVD is an ISO 9660 volume labelled CGJOB whose one file holds the
// runner's configuration, whatever its length.
func TestBuildDVDHoldsTheConfigurationAsItsOneFile(t *testing.T) {
	for name, length := range map[string]int{"short": 120, "a sector": sectorSize, "a byte past a sector": sectorSize + 1, "several sectors": 5000} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			config := strings.Repeat("eyJ", length/3+1)[:length]
			path := filepath.Join(t.TempDir(), "job.iso")

			// Act
			err := BuildDVD(path, config)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			image, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(image[32769:32774], []byte("CD001")) {
				t.Fatalf("byte 32769 holds %q; want the CD001 signature", image[32769:32774])
			}
			label, files, err := readDVD(image)
			if err != nil {
				t.Fatal(err)
			}
			if label != "CGJOB" || len(files) != 1 || string(files[DVDFile]) != config {
				t.Errorf("read label %q and %d file(s), %s holding %d bytes; want CGJOB and the %d-byte configuration alone", label, len(files), DVDFile, len(files[DVDFile]), length)
			}
			fileEnd := (21 + (length+sectorSize-1)/sectorSize) * sectorSize
			if len(image) != fileEnd+150*sectorSize || !bytes.Equal(image[fileEnd:], make([]byte, 150*sectorSize)) {
				t.Errorf("the image is %d bytes; want the file's whole sectors to end at %d, then 150 empty sectors", len(image), fileEnd)
			}
		})
	}
}

// The first boot's seed is an ISO 9660 volume labelled CIDATA, as
// cloud-init's NoCloud finds it, holding the VM's user-data and meta-data
// alone.
func TestBuildSeedHoldsTheFirstBootsSettings(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "seed.iso")
	userData, err := UserData()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	err = BuildSeed(path)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	label, files, err := readDVD(image)
	if err != nil {
		t.Fatal(err)
	}
	if label != "CIDATA" || len(files) != 2 || string(files[seedUserData]) != userData || string(files[seedMetaData]) != MetaData {
		t.Errorf("read label %q and %d file(s); want CIDATA, and the user-data and meta-data the VM is made with alone", label, len(files))
	}
}

// Neither DVD is ever built into a file that is already there, which another
// process could have made, and the file there is left as it was.
func TestBuildingADVDRefusesAPathThatHoldsAFile(t *testing.T) {
	for name, build := range map[string]func(path string) error{
		"a job's":          func(path string) error { return BuildDVD(path, "the next job") },
		"the first boot's": BuildSeed,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "dvd.iso")
			if err := os.WriteFile(path, []byte("made by another"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			err := build(path)

			// Assert
			if image, readErr := os.ReadFile(path); err == nil || readErr != nil || string(image) != "made by another" {
				t.Errorf("building = %v, and the file holds %q (%v); want it refused and the file untouched", err, image, readErr)
			}
		})
	}
}
