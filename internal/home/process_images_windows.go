package home

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processImages returns the program path of every running process this user
// can open. One it cannot open is a system or another user's process, whose
// msys mount, if it has one, is not this user's. This process's own path must
// be among them, or the list proves nothing about what runs.
func processImages() ([]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var images []string
	isSelfSeen := false
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		image, ok := processImage(entry.ProcessID)
		if !ok {
			continue
		}
		images = append(images, image)
		isSelfSeen = isSelfSeen || entry.ProcessID == uint32(os.Getpid())
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	if !isSelfSeen {
		return nil, errors.New("the process list does not show this process")
	}
	return images, nil
}

// userTemp is the temporary folder Windows gives this user's programs when
// nothing redirects them, Temp in the user's local application data, read
// from the user's profile rather than from this process's environment.
func userTemp() (string, error) {
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "Temp"), nil
}

func processImage(pid uint32) (string, bool) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buffer[:size]), true
}
