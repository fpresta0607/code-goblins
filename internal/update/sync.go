package update

import "os"

// writeSynced writes data to path so it survives the machine stopping: the
// data is flushed to disk before the file is renamed into place, and the
// rename itself reaches the disk too (renameSynced). Only the update's journal
// and its verified copies are written this way, because they are the way
// back.
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
	return renameSynced(temporary, path)
}
