package update

import (
	"os"

	"golang.org/x/sys/windows"
)

// writeSynced writes data to path so it survives the machine stopping: the
// data is flushed to disk before the file is renamed into place, and the
// rename itself is written through. Only the update's journal and its
// verified copies are written this way, because they are the way back.
func writeSynced(path string, data []byte) error {
	temporary := path + ".update-tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(temporary)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(temporary)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(temporary)
		return err
	}
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		os.Remove(temporary)
		return err
	}
	return nil
}
