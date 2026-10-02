package voice

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// networking names the Windows libraries a program reaches the network
// through.
var networking = []string{"ws2_32.dll", "wsock32.dll", "mswsock.dll", "winhttp.dll", "wininet.dll", "urlmon.dll", "httpapi.dll", "webio.dll", "dnsapi.dll", "iphlpapi.dll", "rasapi32.dll"}

// offline refuses a folder in which a program or a library is linked against
// a networking library, or cannot be read to find out. What is fetched is
// pinned by its checksum, so this is a statement about those exact files:
// dictation's engine is built with no way to send what it hears.
func offline(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || extension != ".exe" && extension != ".dll" {
			continue
		}
		libraries, err := imports(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("%s cannot be read as a Windows program, so it cannot be checked for networking: %w", entry.Name(), err)
		}
		for _, library := range libraries {
			if slices.Contains(networking, strings.ToLower(library)) {
				return fmt.Errorf("%s links the networking library %s, and the dictation engine must have no way to reach the network", entry.Name(), library)
			}
		}
	}
	return nil
}

// importTables are where a Windows program names the libraries it is linked
// against: its import table and its delay-load table, by index in the data
// directory, with the size of one entry and where in it the library's name
// is. A library whose functions are all imported by number is named there
// too, which the standard library's list of imported symbols leaves out.
var importTables = []struct{ index, size, name int }{
	{pe.IMAGE_DIRECTORY_ENTRY_IMPORT, 20, 12},
	{pe.IMAGE_DIRECTORY_ENTRY_DELAY_IMPORT, 32, 4},
}

// imports returns the libraries a Windows program is linked against.
func imports(file string) ([]string, error) {
	in, err := fsx.Open(file)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	program, err := pe.NewFile(in)
	if err != nil {
		return nil, err
	}
	var directory []pe.DataDirectory
	switch header := program.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		directory = header.DataDirectory[:]
	case *pe.OptionalHeader32:
		directory = header.DataDirectory[:]
	default:
		return nil, errors.New("it has no table of what it links")
	}
	var libraries []string
	for _, table := range importTables {
		address := directory[table.index].VirtualAddress
		for address != 0 {
			entry := read(program, address, table.size)
			if len(entry) < table.size {
				return nil, errors.New("its table of what it links runs off the file")
			}
			if bytes.Equal(entry, make([]byte, table.size)) {
				break
			}
			name, _, ended := bytes.Cut(read(program, binary.LittleEndian.Uint32(entry[table.name:]), 260), []byte{0})
			if !ended || len(name) == 0 {
				return nil, errors.New("a library it links has no readable name")
			}
			libraries = append(libraries, string(name))
			address += uint32(table.size)
		}
	}
	return libraries, nil
}

// read returns up to size bytes of the program at a virtual address, fewer
// where its section ends and none where no section holds the address.
func read(program *pe.File, address uint32, size int) []byte {
	for _, section := range program.Sections {
		if address < section.VirtualAddress || address >= section.VirtualAddress+section.Size {
			continue
		}
		data := make([]byte, size)
		n, _ := section.ReadAt(data, int64(address-section.VirtualAddress))
		return data[:n]
	}
	return nil
}
