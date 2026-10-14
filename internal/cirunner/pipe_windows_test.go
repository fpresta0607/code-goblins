package cirunner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testPipe is a pipe name no other test uses.
func testPipe(t *testing.T) string {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	return `\\.\pipe\code-goblins-ci-runner-test-` + hex.EncodeToString(random[:])
}

// thisUser is the SID of the user the test runs as.
func thisUser(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

// served is a Service on f answering on a pipe of its own, and what it logged.
type served struct {
	service *Service
	name    string
	mu      sync.Mutex
	logged  []string
}

func (s *served) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.logged)
}

// listenAndServe makes the pipe name and serves s on it until ctx ends.
func listenAndServe(ctx context.Context, s *Service, name, allowed string) error {
	pipe, err := Listen(name, allowed)
	if err != nil {
		return err
	}
	return pipe.Serve(ctx, s)
}

// serve answers on a fresh pipe with a Service on f until the test ends, and
// fails the test if Serve does not return once its context ends.
func serve(t *testing.T, f *fakeJob, allowed string) *served {
	t.Helper()
	var config string
	s := &served{name: testPipe(t)}
	s.service = &Service{Deps: testDeps(f, &config), Limits: DefaultLimits, Log: func(line string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.logged = append(s.logged, line)
	}}
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- listenAndServe(ctx, s.service, s.name, allowed) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ended:
			if err != nil {
				t.Errorf("Serve = %v once its context ended; want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return within 10 seconds of its context's end")
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		connection, err := dial(context.Background(), s.name)
		if err == nil {
			connection.Close()
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pipe never opened: %v", err)
		}
	}
}

// ask asks s and returns every reply.
func (s *served) ask(ctx context.Context, kind string) ([]Reply, error) {
	var replies []Reply
	err := Ask(ctx, s.name, Request{Kind: kind}, func(reply Reply) { replies = append(replies, reply) })
	return replies, err
}

// The service answers pending with the job that waits, and status with what
// runs, one reply each.
func TestPipeAnswersPendingAndStatus(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{}, thisUser(t))

	// Act
	pending, pendingErr := s.ask(context.Background(), AskPending)
	status, statusErr := s.ask(context.Background(), AskStatus)

	// Assert
	want := Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)"}
	if pendingErr != nil || len(pending) != 1 || pending[0].Kind != ReplyPending || pending[0].Pending == nil || *pending[0].Pending != want {
		t.Errorf("pending = %+v, %v; want one reply naming %+v", pending, pendingErr, want)
	}
	if statusErr != nil || len(status) != 1 || status[0].Kind != ReplyStatus || status[0].Line != "no job has run since the service started" {
		t.Errorf("status = %+v, %v; want one line saying no job has run", status, statusErr)
	}
}

// A run's progress comes as it happens, and its one done reply carries its
// outcome, which the service logs.
func TestPipeRunStreamsProgressAndEndsWithItsOutcome(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{busyAt: 2, offAt: 5}, thisUser(t))

	// Act
	replies, err := s.ask(context.Background(), AskRun)

	// Assert
	var kinds []string
	for _, reply := range replies {
		kinds = append(kinds, reply.Kind)
	}
	if err != nil || !slices.Equal(kinds, []string{ReplyProgress, ReplyProgress, ReplyProgress, ReplyDone}) || replies[len(replies)-1].Outcome != Ran {
		t.Errorf("run = %+v, %v; want three progress lines, then done with ran", replies, err)
	}
	if lines := s.lines(); !slices.Contains(lines, "the run ended: ran") {
		t.Errorf("the service logged %q; want the run's outcome", lines)
	}
}

// A client that closes the pipe during its run ends the job, as the end of
// the turn it runs in, and the VM is put back and the runner deleted.
func TestClosingThePipeAbandonsTheJob(t *testing.T) {
	// Arrange
	f := &fakeJob{busyAt: 2}
	s := serve(t, f, thisUser(t))
	advance := s.service.Deps.Sleep
	s.service.Deps.Sleep = func(ctx context.Context, step time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
		advance(ctx, step)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	err := Ask(ctx, s.name, Request{Kind: AskRun}, func(reply Reply) {
		if reply.Kind == ReplyProgress {
			cancel()
		}
	})

	// Assert
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Ask = %v; want it ended with its context", err)
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(s.service.Status(), "ended with its turn"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("status %q 10 seconds after the pipe closed; want the job ended with its turn", s.service.Status())
		}
	}
	if calls := f.callsMade(); !slices.Equal(calls[len(calls)-3:], []string{"off", "restore", "delete"}) {
		t.Errorf("the job's last calls were %q; want the VM off, back at clean, and the runner deleted", calls)
	}
}

// A client that connects and sends nothing is dropped within 5 seconds, and
// another client is answered while it waits.
func TestServiceDropsAClientThatSendsNothing(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{}, thisUser(t))
	silent, err := dial(context.Background(), s.name)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	connected := time.Now()

	// Act
	status, statusErr := s.ask(context.Background(), AskStatus)
	answered := time.Since(connected)
	_ = silent.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, readErr := silent.Read(make([]byte, 1))
	dropped := time.Since(connected)

	// Assert
	if statusErr != nil || len(status) != 1 || answered > 2*time.Second {
		t.Errorf("status = %+v, %v after %v; want it answered while the silent client waits", status, statusErr, answered)
	}
	if readErr == nil || errors.Is(readErr, windows.ERROR_TIMEOUT) || strings.Contains(readErr.Error(), "timeout") || dropped < requestLimit-time.Second || dropped > requestLimit+2*time.Second {
		t.Errorf("the silent client read %v after %v; want it dropped at %v", readErr, dropped, requestLimit)
	}
}

// What the service cannot answer gets one error reply and changes nothing.
func TestPipeRefusesWhatItCannotAnswer(t *testing.T) {
	for name, line := range map[string]string{
		"a kind it does not know":    `{"kind":"delete"}`,
		"a request that is not JSON": `delete everything`,
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{}
			s := serve(t, f, thisUser(t))
			connection, err := dial(context.Background(), s.name)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			_ = connection.SetDeadline(time.Now().Add(10 * time.Second))

			// Act
			_, writeErr := connection.Write([]byte(line + "\n"))
			answer, readErr := bufio.NewReader(connection).ReadBytes('\n')

			// Assert
			var reply Reply
			if writeErr != nil || readErr != nil || json.Unmarshal(answer, &reply) != nil || reply.Kind != ReplyError || reply.Error == "" {
				t.Errorf("answered %q, %v, %v; want one error reply", answer, writeErr, readErr)
			}
			if calls := f.callsMade(); len(calls) != 0 {
				t.Errorf("the refusal made calls %q; want none", calls)
			}
		})
	}
}

// A request line past 4 KiB is dropped unanswered, as soon as it passes.
func TestPipeDropsARequestPastItsLimit(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{}, thisUser(t))
	connection, err := dial(context.Background(), s.name)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))

	// Act
	sent := time.Now()
	_, writeErr := connection.Write([]byte(strings.Repeat("a", maxRequest+1000)))
	answer, readErr := bufio.NewReader(connection).ReadBytes('\n')
	dropped := time.Since(sent)

	// Assert
	if writeErr != nil || readErr == nil || len(answer) != 0 || strings.Contains(readErr.Error(), "timeout") || dropped > requestLimit/2 {
		t.Errorf("answered %q, %v, %v after %v; want the client dropped unanswered at once", answer, writeErr, readErr, dropped)
	}
}

// Asking where no service answers says so at once.
func TestAskSaysTheServiceIsNotRunning(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	err := Ask(ctx, testPipe(t), Request{Kind: AskStatus}, func(Reply) {})

	// Assert
	if !errors.Is(err, ErrNotServing) {
		t.Errorf("Ask = %v; want ErrNotServing", err)
	}
}

// An allowed user that is no SID is refused before any pipe is made, so no
// text of its own reaches the pipe's security.
func TestServeRefusesAnAllowedUserThatIsNoSID(t *testing.T) {
	// Arrange
	name := testPipe(t)
	var config string
	s := &Service{Deps: testDeps(&fakeJob{}, &config), Limits: DefaultLimits, Log: func(string) {}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Act
	err := listenAndServe(ctx, s, name, thisUser(t)+")(A;;GA;;;WD")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "is not a SID") {
		t.Errorf("Serve = %v; want the allowed user refused", err)
	}
	if _, err := dial(context.Background(), name); !errors.Is(err, ErrNotServing) {
		t.Errorf("dial = %v; want no pipe made", err)
	}
}

// The pipe lets SYSTEM, Administrators and the service's own user open it,
// and the fleet's user read and write it, and no one else. The fleet's user
// is admitted by its SID, whatever name Windows gives it in SDDL: here this
// machine's own Administrator, as a CI runner's account is, which reads
// back as LA.
func TestPipeAdmitsOnlyItsUsers(t *testing.T) {
	// Arrange
	administrator, err := windows.StringToSid("LA")
	if err != nil {
		t.Fatal(err)
	}
	s := serve(t, &fakeJob{}, administrator.String())
	connection, err := dial(context.Background(), s.name)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	// Act
	descriptor, err := windows.GetSecurityInfo(windows.Handle(connection.Fd()), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.LABEL_SECURITY_INFORMATION)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	dacl, label, _ := strings.Cut(descriptor.String(), "S:")
	if !strings.HasSuffix(label, "(ML;;NW;;;ME)") || strings.Count(label, "(") != 1 {
		t.Errorf("the pipe's label is %q; want medium integrity alone, so the fleet opens it", label)
	}
	if !strings.Contains(dacl, ";;;LA)") {
		t.Fatalf("the pipe's DACL is %s; want the machine's Administrator named LA, as this test's premise", dacl)
	}
	if got, want := pipeEntries(t, descriptor), []string{"S-1-5-18=0x1f01ff", "S-1-5-32-544=0x1f01ff", thisUser(t) + "=0x1f01ff", administrator.String() + "=" + fleetAccess}; !slices.Equal(got, sorted(want)) {
		t.Errorf("the pipe's DACL gives %q in %s; want %q alone", got, descriptor, sorted(want))
	}
}

// What the fleet's user may do opens the pipe and makes no instance of it,
// so the fleet can ask the service and never answer in its place.
func TestTheFleetsAccessOpensThePipeAndMakesNoInstance(t *testing.T) {
	// Arrange
	name := testPipe(t)
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;" + fleetAccess + ";;;" + thisUser(t) + ")")
	if err != nil {
		t.Fatal(err)
	}
	pipe := &Pipe{text: name, name: path, security: &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}}
	if pipe.waiting, err = pipe.instance(true); err != nil {
		t.Fatal(err)
	}
	defer pipe.close()

	// Act
	connection, dialErr := dial(context.Background(), name)
	another, instanceErr := pipe.instance(false)

	// Assert
	if dialErr != nil {
		t.Errorf("dial = %v; want the fleet's access to open the pipe", dialErr)
	} else {
		connection.Close()
	}
	if instanceErr == nil {
		windows.CloseHandle(another)
		t.Error("the fleet's access made another instance of the pipe; want it refused")
	} else if !errors.Is(instanceErr, windows.ERROR_ACCESS_DENIED) {
		t.Errorf("another instance = %v; want access denied", instanceErr)
	}
}

// Begin takes the pipe before the service puts back the VM, so while the
// service starts, which may take minutes, no other process can make the
// pipe and answer for it.
func TestBeginHoldsThePipeWhileTheServiceStarts(t *testing.T) {
	// Arrange
	f := &fakeJob{offHeld: make(chan struct{})}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits, Log: func(string) {}}
	name := testPipe(t)
	begun := make(chan error, 1)
	go func() {
		pipe, err := Begin(context.Background(), s, name, thisUser(t))
		if err == nil {
			err = pipe.close()
		}
		begun <- err
	}()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := dial(context.Background(), name); !errors.Is(err, ErrNotServing) || time.Now().After(deadline) {
			break
		}
	}

	// Act
	squatter, err := Listen(name, thisUser(t))
	close(f.offHeld)

	// Assert
	if err == nil {
		squatter.close()
		t.Error("another process made the pipe while the service started; want it refused")
	}
	if err := <-begun; err != nil {
		t.Errorf("Begin = %v; want the service started on its pipe", err)
	}
	if calls := f.callsMade(); !slices.Equal(calls, []string{"off", "restore", "delete own"}) {
		t.Errorf("the service's start made %q; want the VM put back and its runners deleted", calls)
	}
}

// The fleet's user must be one user's account. A group or a well-known SID
// would let every member open the pipe.
func TestListenRefusesAGroupAsTheFleetsUser(t *testing.T) {
	for _, sid := range []string{"S-1-1-0", "S-1-5-11", "S-1-5-32-545", "S-1-5-32-547"} {
		t.Run(sid, func(t *testing.T) {
			// Arrange
			name := testPipe(t)

			// Act
			pipe, err := Listen(name, sid)

			// Assert
			if err == nil {
				pipe.close()
				t.Fatalf("Listen for %s made the pipe; want it refused", sid)
			}
			if !strings.Contains(err.Error(), "names no one user's account") {
				t.Errorf("Listen = %v; want the SID refused as no user's account", err)
			}
		})
	}
}

// A refused request is logged in the service's own words, never the
// request's, and at most once a minute, so a client sending what the
// service refuses without end cannot push a job's lines out of the log.
func TestRefusalsAreLoggedInTheServicesWordsAtMostOnceAMinute(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{}, thisUser(t))
	junk := strings.Repeat("x", 3000)

	// Act
	for range 20 {
		if err := Ask(context.Background(), s.name, Request{Kind: junk}, func(Reply) {}); err == nil {
			t.Fatal("a request of no kind the service answers was answered")
		}
	}

	// Assert
	lines := s.lines()
	refusals := slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return !strings.Contains(line, "refused") })
	if len(refusals) != 1 || strings.Contains(strings.Join(lines, "\n"), junk[:20]) {
		t.Errorf("the log holds %q; want one refusal line in the service's own words", lines)
	}
}

// pipeEntries reads every entry of a protected DACL as its SID and access
// mask, sorted, and fails the test on a DACL that is not protected or an
// entry that is not a plain allowing one, which an entry by entry reading
// would otherwise pass over.
func pipeEntries(t *testing.T, descriptor *windows.SECURITY_DESCRIPTOR) []string {
	t.Helper()
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the pipe's DACL in %s is not protected, so it may inherit entries", descriptor)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("the pipe has no DACL to read: %v", err)
	}
	var entries []string
	for index := range uint32(dacl.AceCount) {
		var entry *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != 0 {
			t.Errorf("the pipe's DACL in %s holds an entry of type %d with flags %#x; want plain allowing entries alone", descriptor, entry.Header.AceType, entry.Header.AceFlags)
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
		entries = append(entries, fmt.Sprintf("%s=%#x", sid.String(), uint32(entry.Mask)))
	}
	return sorted(entries)
}

func sorted(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return values
}

// A pipe another process already serves is refused, so nothing stands in for
// a running service by making its pipe first.
func TestServeRefusesAPipeAnotherAlreadyServes(t *testing.T) {
	// Arrange
	first := serve(t, &fakeJob{}, thisUser(t))
	var config string
	second := &Service{Deps: testDeps(&fakeJob{}, &config), Limits: DefaultLimits, Log: func(string) {}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ended := make(chan error, 1)

	// Act
	go func() { ended <- listenAndServe(ctx, second, first.name, thisUser(t)) }()

	// Assert
	select {
	case err := <-ended:
		if err == nil || !strings.Contains(err.Error(), "create pipe") {
			t.Errorf("a second Serve = %v; want the pipe refused", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("a second Serve on the same pipe served it")
	}
}

// A service told to stop during a job ends the job, and returns only once
// the VM is back at clean and the runner deleted.
func TestServeEndsARunningJobBeforeItReturns(t *testing.T) {
	// Arrange
	f := &fakeJob{busyAt: 2, deleting: 300 * time.Millisecond}
	name := testPipe(t)
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits, Log: func(string) {}}
	advance := s.Deps.Sleep
	s.Deps.Sleep = func(ctx context.Context, step time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
		advance(ctx, step)
	}
	ctx, stop := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- listenAndServe(ctx, s, name, thisUser(t)) }()
	running := make(chan struct{})
	go func() {
		var once sync.Once
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			err := Ask(context.Background(), name, Request{Kind: AskRun}, func(reply Reply) {
				if reply.Kind == ReplyProgress {
					once.Do(func() { close(running) })
				}
			})
			if !errors.Is(err, ErrNotServing) {
				return
			}
		}
	}()
	<-running

	// Act
	stop()
	err := <-ended

	// Assert
	if calls := f.callsMade(); err != nil || !slices.Equal(calls[len(calls)-3:], []string{"off", "restore", "delete"}) {
		t.Errorf("Serve = %v with the job's last calls %q; want it to return after the VM is off, back at clean, and the runner deleted", err, calls)
	}
}

// A refusal reaches Ask's caller as an error naming it, never as a reply.
func TestAskReturnsARefusalAsAnError(t *testing.T) {
	// Arrange
	s := serve(t, &fakeJob{}, thisUser(t))
	var replies []Reply

	// Act
	err := Ask(context.Background(), s.name, Request{Kind: "delete"}, func(reply Reply) { replies = append(replies, reply) })

	// Assert
	if err == nil || !strings.Contains(err.Error(), `the service refused: the service answers pending, run and status, not "delete"`) || len(replies) != 0 {
		t.Errorf("Ask = %v with replies %+v; want the refusal as an error alone", err, replies)
	}
}
