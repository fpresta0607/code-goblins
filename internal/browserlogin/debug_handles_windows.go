package browserlogin

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"

	"golang.org/x/sys/windows"
)

func debugHandles(command *exec.Cmd) (*devToolsBridge, func(), error) {
	read, input, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	output, write, err := os.Pipe()
	if err != nil {
		read.Close()
		input.Close()
		return nil, nil, err
	}
	closeChildren := func() { read.Close(); write.Close() }
	for _, file := range []*os.File{input, output} {
		if err := windows.SetHandleInformation(windows.Handle(file.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			closeChildren()
			input.Close()
			output.Close()
			return nil, nil, err
		}
	}
	for _, file := range []*os.File{read, write} {
		if err := windows.SetHandleInformation(windows.Handle(file.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			closeChildren()
			input.Close()
			output.Close()
			return nil, nil, err
		}
	}
	command.SysProcAttr.AdditionalInheritedHandles = append(command.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(read.Fd()), syscall.Handle(write.Fd()))
	command.Args = slices.Insert(command.Args, 1, fmt.Sprintf("--remote-debugging-io-pipes=%d,%d", read.Fd(), write.Fd()))
	return &devToolsBridge{pipe: newDevToolsPipe(output, input)}, closeChildren, nil
}
