package supervisor

import (
	"encoding/binary"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemPoolTagInformation is the class of NtQuerySystemInformation that
// lists the kernel's pool tags, which a process run without administrator
// rights may read.
const systemPoolTagInformation = 22

// poolTagSize is the size of Windows' SYSTEM_POOLTAG on a 64-bit kernel: the
// tag, then the paged pool's allocations, frees and bytes, then the nonpaged
// pool's, each count four bytes and each size eight.
const poolTagSize = 40

// PoolTags reads how much of the kernel's paged and nonpaged pools each pool
// tag holds, from one system list. A tag names the driver or kernel part that
// allocated the memory, as poolmon shows it.
func PoolTags() ([]PoolTag, error) {
	buffer := make([]byte, 1<<20)
	for {
		var needed uint32
		err := windows.NtQuerySystemInformation(systemPoolTagInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &needed)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, err
		}
		// Tags are added as drivers allocate under new ones.
		buffer = make([]byte, max(int(needed), 2*len(buffer)))
	}
	// The count is followed by the entries, which start eight bytes in.
	count := int(binary.LittleEndian.Uint32(buffer))
	if 8+count*poolTagSize > len(buffer) {
		return nil, errors.New("the kernel's list of pool tags is shorter than its count")
	}
	tags := make([]PoolTag, 0, count)
	for index := range count {
		entry := buffer[8+index*poolTagSize:]
		tags = append(tags, PoolTag{
			Tag:            string(entry[0:4]),
			PagedAllocs:    binary.LittleEndian.Uint32(entry[4:8]),
			PagedFrees:     binary.LittleEndian.Uint32(entry[8:12]),
			Paged:          binary.LittleEndian.Uint64(entry[16:24]),
			NonpagedAllocs: binary.LittleEndian.Uint32(entry[24:28]),
			Nonpaged:       binary.LittleEndian.Uint64(entry[32:40]),
		})
	}
	return tags, nil
}
