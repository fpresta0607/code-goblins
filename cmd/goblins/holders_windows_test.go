package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	restartManager      = windows.NewLazySystemDLL("rstrtmgr.dll")
	rmStartSession      = restartManager.NewProc("RmStartSession")
	rmEndSession        = restartManager.NewProc("RmEndSession")
	rmRegisterResources = restartManager.NewProc("RmRegisterResources")
	rmGetList           = restartManager.NewProc("RmGetList")
)

// rmProcessInfo is Windows' RM_PROCESS_INFO.
type rmProcessInfo struct {
	PID              uint32
	Started          windows.Filetime
	AppName          [256]uint16
	ServiceShortName [64]uint16
	ApplicationType  uint32
	AppStatus        uint32
	SessionID        uint32
	Restartable      int32
}

// holdersOf names the processes Windows' Restart Manager finds holding path
// open, for a test whose rename or removal of path was refused: a refusal
// says only that "another process" holds the file. Restart Manager sees the
// handles of processes, so a holder it does not find, such as a virus
// scanner's filter driver reading the file, is named as none it can see.
func holdersOf(path string) string {
	var session uint32
	key := make([]uint16, 64)
	if code, _, _ := rmStartSession.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0]))); code != 0 {
		return fmt.Sprintf("its holders could not be asked (RmStartSession %d)", code)
	}
	defer rmEndSession.Call(uintptr(session))
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "its holders could not be asked: " + err.Error()
	}
	if code, _, _ := rmRegisterResources.Call(uintptr(session), 1, uintptr(unsafe.Pointer(&name)), 0, 0, 0, 0); code != 0 {
		return fmt.Sprintf("its holders could not be asked (RmRegisterResources %d)", code)
	}
	var needed, reasons uint32
	infos := make([]rmProcessInfo, 16)
	count := uint32(len(infos))
	if code, _, _ := rmGetList.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&infos[0])), uintptr(unsafe.Pointer(&reasons))); code != 0 {
		return fmt.Sprintf("its holders could not be listed (RmGetList %d, %d of them)", code, needed)
	}
	if count == 0 {
		return "no process Restart Manager can see holds it"
	}
	var holders []string
	for _, info := range infos[:count] {
		holders = append(holders, fmt.Sprintf("pid %d %s", info.PID, windows.UTF16ToString(info.AppName[:])))
	}
	return "held by " + strings.Join(holders, ", ")
}
