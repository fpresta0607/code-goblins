package supervisor

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

var (
	kernel32                        = syscall.NewLazyDLL("kernel32.dll")
	procCreateNamedPipeW            = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe            = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe         = kernel32.NewProc("DisconnectNamedPipe")
	procGetNamedPipeClientProcessID = kernel32.NewProc("GetNamedPipeClientProcessId")
	procGetNamedPipeServerProcessID = kernel32.NewProc("GetNamedPipeServerProcessId")
	procConvertSDDL                 = syscall.NewLazyDLL("advapi32.dll").NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

const (
	pipeAccessDuplex        = 0x00000003
	fileFlagFirstInstance   = 0x00080000
	pipeRejectRemoteClients = 0x00000008
	pipeUnlimitedInstances  = 255
	errorPipeConnected      = syscall.Errno(535)
	errorPipeBusy           = syscall.Errno(231)
	// maxRunRequest bounds one request: the command plus its JSON escaping.
	maxRunRequest = 8 * maxRunCommand
	// runReadTimeout bounds how long a client takes to send its request, and
	// runReplyTimeout how long cfo run-request waits for the answer.
	runReadTimeout  = 10 * time.Second
	runReplyTimeout = 30 * time.Second
)

// runPipeName is the supervisor's pipe for run requests, one per state
// directory, so two homes never share one.
func runPipeName(stateDir string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(stateDir))))
	return `\\.\pipe\cfo-run-requests-` + hex.EncodeToString(sum[:8])
}

// firstInstanceWait is how long the supervisor waits for a pipe name another
// instance still holds to come free: a supervisor that just stopped releases
// its instances within moments, and a name held longer than this is not ours.
const firstInstanceWait = 2 * time.Second

// serveRunRequests takes run items over the supervisor's named pipe until ctx
// ends. Every client is proven by its own process: only one that runs under
// the registered primary CFO gets an item onto the board. The pipe is the
// supervisor's own from its first instance, or it is not served at all: a
// process that created the name first, another local user's included, would
// otherwise receive every request, and only the current user may open or add
// to it.
func (s *Service) serveRunRequests(ctx context.Context) {
	name, err := syscall.UTF16PtrFromString(runPipeName(s.Store.Home.State))
	if err != nil {
		s.publish(fmt.Errorf("run requests: %w", err))
		return
	}
	attributes, err := currentUserOnly()
	if err != nil {
		s.publish(fmt.Errorf("run requests: %w", err))
		return
	}
	started := time.Now()
	done := make(chan struct{})
	defer close(done)
	go func() {
		// A pending ConnectNamedPipe returns only for a client, so clients are
		// sent until the loop has ended.
		<-ctx.Done()
		for {
			if wake, err := os.OpenFile(runPipeName(s.Store.Home.State), os.O_RDWR, 0); err == nil {
				_ = wake.Close()
			}
			select {
			case <-done:
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	}()
	first := true
	for ctx.Err() == nil {
		mode := uintptr(pipeAccessDuplex)
		if first {
			mode |= fileFlagFirstInstance
		}
		handle, _, callErr := procCreateNamedPipeW.Call(uintptr(unsafe.Pointer(name)), mode, pipeRejectRemoteClients, pipeUnlimitedInstances, 64<<10, 64<<10, 0, uintptr(unsafe.Pointer(attributes)))
		if syscall.Handle(handle) == syscall.InvalidHandle && first && errors.Is(callErr, syscall.ERROR_ACCESS_DENIED) && time.Since(started) < firstInstanceWait {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if syscall.Handle(handle) == syscall.InvalidHandle {
			if first && errors.Is(callErr, syscall.ERROR_ACCESS_DENIED) {
				err := fmt.Errorf("run requests: another process already holds the pipe %s, so run requests are not served; stop it and restart cfo serve", runPipeName(s.Store.Home.State))
				s.Store.mu.Lock()
				s.Store.issue(err.Error())
				saveErr := s.Store.save()
				s.Store.mu.Unlock()
				s.publish(errors.Join(err, saveErr))
				return
			}
			s.publish(fmt.Errorf("run requests: the pipe could not be created: %w", callErr))
			return
		}
		first = false
		ok, _, callErr := procConnectNamedPipe.Call(handle, 0)
		connected := time.Now()
		if ok == 0 && !errors.Is(callErr, errorPipeConnected) || ctx.Err() != nil {
			_ = syscall.CloseHandle(syscall.Handle(handle))
			continue
		}
		go s.handleRunClient(syscall.Handle(handle), connected)
	}
}

// handleRunClient answers one run request with the reason it was refused, or
// nothing when the item is on the board. A client that has not sent its
// request within runReadTimeout is disconnected unanswered.
func (s *Service) handleRunClient(handle syscall.Handle, connected time.Time) {
	var pid uint32
	ok, _, callErr := procGetNamedPipeClientProcessID.Call(uintptr(handle), uintptr(unsafe.Pointer(&pid)))
	pipe := os.NewFile(uintptr(handle), "run request pipe")
	defer func() {
		_, _, _ = procDisconnectNamedPipe.Call(uintptr(handle))
		_ = pipe.Close()
	}()
	var reply struct {
		Error string `json:"error,omitempty"`
	}
	expired := make(chan struct{})
	timer := time.AfterFunc(runReadTimeout, func() {
		_ = syscall.CancelIoEx(handle, nil)
		close(expired)
	})
	line, err := bufio.NewReader(io.LimitReader(pipe, maxRunRequest)).ReadBytes('\n')
	if !timer.Stop() {
		<-expired
		return
	}
	var req runPipeRequest
	switch {
	case ok == 0:
		reply.Error = "the supervisor could not tell which process sent the request: " + callErr.Error()
	case err != nil:
		reply.Error = "the run request was incomplete or over its size limit"
	case json.Unmarshal(line, &req) != nil:
		reply.Error = "the run request is not valid JSON"
	default:
		s.runRequests.Lock()
		if req.Kind == "" {
			err = s.acceptRunRequest(int(pid), connected, req)
		} else {
			err = s.acceptCFOItem(int(pid), connected, req)
		}
		s.runRequests.Unlock()
		if err != nil {
			reply.Error = err.Error()
		} else {
			s.publish(nil)
		}
	}
	data, _ := json.Marshal(reply)
	_, _ = pipe.Write(append(data, '\n'))
}

// sendPipeRequest hands one request to the supervisor and returns the reason
// it was refused, if it was.
func sendPipeRequest(stateDir string, req runPipeRequest) error {
	var pipe *os.File
	var err error
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		pipe, err = os.OpenFile(runPipeName(stateDir), os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
		if !errors.Is(err, errorPipeBusy) && !errors.Is(err, os.ErrNotExist) || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		return errors.New("the supervisor is not running, so the board cannot take this; start cfo serve")
	}
	defer pipe.Close()
	// The command goes only to the supervisor of this home: the process that
	// holds its watch lock. A pipe of that name served by anything else is a
	// squatter, and it never sees the request.
	var server uint32
	if ok, _, callErr := procGetNamedPipeServerProcessID.Call(pipe.Fd(), uintptr(unsafe.Pointer(&server))); ok == 0 {
		return fmt.Errorf("the run request pipe's server could not be identified: %w", callErr)
	}
	if !lock.HeldByNamed(stateDir, ".watch.lock", int(server)) {
		return fmt.Errorf("the run request pipe is served by pid %d, which is not this home's supervisor, so nothing was sent", server)
	}
	if err := pipe.SetDeadline(time.Now().Add(runReplyTimeout)); err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := pipe.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("the supervisor did not take the run request: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(pipe, 64<<10)).ReadBytes('\n')
	var reply struct {
		Error string `json:"error"`
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("the supervisor did not answer the run request within %s", runReplyTimeout)
	}
	if err != nil || json.Unmarshal(line, &reply) != nil {
		return errors.New("the supervisor gave no answer to the run request")
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// currentUserOnly is a security descriptor that grants the current user, and
// nobody else, every right on the pipe: to connect, and to add an instance.
func currentUserOnly() (*syscall.SecurityAttributes, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return nil, err
	}
	sddl, err := syscall.UTF16PtrFromString("D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return nil, err
	}
	var descriptor uintptr
	if ok, _, callErr := procConvertSDDL.Call(uintptr(unsafe.Pointer(sddl)), 1, uintptr(unsafe.Pointer(&descriptor)), 0); ok == 0 {
		return nil, fmt.Errorf("the pipe's security descriptor could not be built: %w", callErr)
	}
	return &syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: descriptor}, nil
}
