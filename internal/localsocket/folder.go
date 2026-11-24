//go:build !windows

package localsocket

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// Folder is the folder that holds this home's sockets: one of the user's
// own, named by a short hash of the home, under /run/user/<uid> where that
// folder is the user's own, as systemd makes it on Linux, or else under
// /tmp, as on macOS. It is chosen by the user's ID, not by XDG_RUNTIME_DIR,
// so a command run from cron, su - or sudo -iu, which has no such variable,
// still finds the supervisor the user started. A socket's path must fit
// sun_path, 108 bytes on Linux and 104 on macOS, which a long home or a
// test's temporary folder overflows, so the sockets are not in the home.
func Folder(stateDir string) string {
	uid := os.Getuid()
	base := "/tmp"
	runtime := filepath.Join("/run/user", strconv.Itoa(uid))
	if info, err := os.Stat(runtime); err == nil && info.IsDir() && isOwnedBy(info, uid) {
		base = runtime
	}
	sum := sha256.Sum256([]byte(filepath.Clean(stateDir)))
	return filepath.Join(base, "code-goblins-"+hex.EncodeToString(sum[:8]))
}

// isOwnedBy reports whether uid owns the file info describes.
func isOwnedBy(info os.FileInfo, uid int) bool {
	stat, isStat := info.Sys().(*syscall.Stat_t)
	return isStat && int(stat.Uid) == uid
}

// CheckFolder refuses folder unless it is a folder, not a link to one, that
// this user owns and nobody else may enter: under /tmp anyone may make a
// folder of that name first.
func CheckFolder(folder string) error {
	info, err := os.Lstat(folder)
	if err != nil {
		return err
	}
	if !info.IsDir() || !isOwnedBy(info, os.Getuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("the socket folder %s is not this user's own with mode 0700, so it is not used", folder)
	}
	return nil
}
