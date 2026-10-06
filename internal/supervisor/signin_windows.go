package supervisor

import (
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	lsaGetLogonSessionData = windows.NewLazySystemDLL("secur32.dll").NewProc("LsaGetLogonSessionData")
	lsaFreeReturnBuffer    = windows.NewLazySystemDLL("secur32.dll").NewProc("LsaFreeReturnBuffer")
)

// tokenStatistics is Windows' TOKEN_STATISTICS.
type tokenStatistics struct {
	tokenID            windows.LUID
	authenticationID   windows.LUID
	expirationTime     int64
	tokenType          uint32
	impersonationLevel uint32
	dynamicCharged     uint32
	dynamicAvailable   uint32
	groupCount         uint32
	privilegeCount     uint32
	modifiedID         windows.LUID
}

// logonSessionData is the start of Windows' SECURITY_LOGON_SESSION_DATA, up
// to the time of the logon.
type logonSessionData struct {
	size                  uint32
	logonID               windows.LUID
	userName              windows.NTUnicodeString
	logonDomain           windows.NTUnicodeString
	authenticationPackage windows.NTUnicodeString
	logonType             uint32
	session               uint32
	sid                   *windows.SID
	logonTime             int64
}

// SignedIn is when the Windows sign-in this process runs in began: a restart
// and a sign-out both end every process of the sign-in before it, so a
// terminal that started before it was ended by one of them.
func SignedIn() (time.Time, error) {
	var statistics tokenStatistics
	var length uint32
	token := windows.GetCurrentProcessToken()
	if err := windows.GetTokenInformation(token, windows.TokenStatistics, (*byte)(unsafe.Pointer(&statistics)), uint32(unsafe.Sizeof(statistics)), &length); err != nil {
		return time.Time{}, fmt.Errorf("read this sign-in's logon session: %w", err)
	}
	var data *logonSessionData
	status, _, _ := lsaGetLogonSessionData.Call(uintptr(unsafe.Pointer(&statistics.authenticationID)), uintptr(unsafe.Pointer(&data)))
	if status != 0 {
		return time.Time{}, fmt.Errorf("read when this sign-in began: %w", windows.NTStatus(status))
	}
	defer lsaFreeReturnBuffer.Call(uintptr(unsafe.Pointer(data)))
	if data.logonTime <= 0 {
		return time.Time{}, fmt.Errorf("this sign-in's logon session names no time it began")
	}
	began := windows.Filetime{LowDateTime: uint32(data.logonTime), HighDateTime: uint32(data.logonTime >> 32)}
	return time.Unix(0, began.Nanoseconds()).UTC(), nil
}
