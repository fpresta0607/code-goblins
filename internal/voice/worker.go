package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// The engine runs in `cfo voice-worker`, a child of the process dictating: it
// loads the engine's library and the model once, then reads one sound at a
// time from its standard input and writes the words to its standard output,
// each as a frame of a 4-byte little-endian length and that many bytes. A
// fault in the engine ends the worker, never the board, and its memory goes
// back to the machine when it is ended.
const (
	// MAX_SOUND_BYTES bounds one sound, as the board's own limit does.
	MAX_SOUND_BYTES = 8 << 20
	// MAX_REPLY_BYTES bounds one reply of the worker.
	MAX_REPLY_BYTES = 1 << 20
)

// workerIdle is how long a loaded engine waits for the next dictation before
// it is ended.
var workerIdle = 2 * time.Minute

// workerEnvironment is all of this process's environment the worker gets:
// what Windows needs to start a program and find its temporary folder, and no
// token or other setting of the fleet.
var workerEnvironment = []string{"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "PATHEXT", "TEMP", "TMP"}

var errClosed = errors.New("dictation is closed")

// workerReply is the worker's answer to one sound: its words, or why the
// engine could not recognise it.
type workerReply struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// worker is one running `cfo voice-worker`.
type worker struct {
	command *exec.Cmd
	end     context.CancelFunc
	input   io.Writer
	output  io.Reader
	// log is what the worker wrote on its standard error, read only once it
	// has exited.
	log    bytes.Buffer
	exited chan struct{}
	idle   *time.Timer
	// uses counts the dictations it answered, so an idle timer that fired
	// while another dictation had the turn knows it is out of date.
	uses int
}

func (v *Voice) initialize() {
	v.start.Do(func() {
		v.turn = make(chan struct{}, 1)
		v.turn <- struct{}{}
		v.closed = make(chan struct{})
	})
}

// take waits for the turn, unless ctx ends or dictation closes first.
func (v *Voice) take(ctx context.Context) error {
	v.initialize()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.closed:
		return errClosed
	case <-v.turn:
	}
	// The turn may have come at the same moment as either.
	if err := ctx.Err(); err != nil {
		v.give()
		return err
	}
	select {
	case <-v.closed:
		v.give()
		return errClosed
	default:
	}
	return nil
}

func (v *Voice) give() { v.turn <- struct{}{} }

// retire ends the worker, if one runs, and waits until it has exited. Only
// the holder of the turn calls it.
func (v *Voice) retire() {
	if v.worker == nil {
		return
	}
	if v.worker.idle != nil {
		v.worker.idle.Stop()
	}
	v.worker.end()
	<-v.worker.exited
	v.worker = nil
}

// Close ends the worker and refuses dictation from then on. A dictation in
// progress ends without its words.
func (v *Voice) Close() {
	v.initialize()
	v.closing.Do(func() { close(v.closed) })
	<-v.turn
	v.retire()
	v.give()
}

func (v *Voice) startWorker() (*worker, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	arguments := []string{"voice-worker", filepath.Join(v.folder(v.Settings.Engine), v.Settings.Program)}
	for _, argument := range v.Settings.Args {
		arguments = append(arguments, strings.ReplaceAll(argument, "{model}", v.folder(v.Settings.Model)))
	}
	ctx, end := context.WithCancel(context.Background())
	command := execx.CommandContext(ctx, self, arguments...)
	command.Dir = v.folder(v.Settings.Engine)
	command.Env = []string{}
	for _, name := range workerEnvironment {
		if value, found := os.LookupEnv(name); found {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	w := &worker{command: command, end: end, exited: make(chan struct{})}
	command.Stderr = &w.log
	if w.input, err = command.StdinPipe(); err == nil {
		w.output, err = command.StdoutPipe()
	}
	if err == nil {
		err = command.Start()
	}
	if err != nil {
		end()
		return nil, err
	}
	go func() {
		_ = command.Wait()
		close(w.exited)
	}()
	return w, nil
}

// exchange hands sound to the worker, starting one when none runs or the last
// one has exited, and returns its words. A worker that fails or is abandoned
// is ended; one whose engine could not recognise the sound is kept.
func (v *Voice) exchange(ctx context.Context, sound []byte) (string, error) {
	if v.worker != nil {
		select {
		case <-v.worker.exited:
			v.retire()
		default:
			v.worker.idle.Stop()
		}
	}
	if v.worker == nil {
		w, err := v.startWorker()
		if err != nil {
			return "", fmt.Errorf("start the dictation engine: %w", err)
		}
		v.worker = w
	}
	w := v.worker
	answer := make(chan workerAnswer, 1)
	go func() { answer <- w.ask(sound) }()
	var reply workerAnswer
	select {
	case <-ctx.Done():
		v.retire()
		return "", ctx.Err()
	case <-v.closed:
		v.retire()
		return "", errClosed
	case reply = <-answer:
	}
	if reply.err != nil {
		v.retire()
		return "", fmt.Errorf("the dictation engine failed: %w: %s", reply.err, lastLines(w.log.Bytes(), 3))
	}
	w.uses++
	uses := w.uses
	w.idle = time.AfterFunc(workerIdle, func() { v.idleOut(w, uses) })
	if reply.Error != "" {
		return "", fmt.Errorf("the dictation engine could not recognise the sound: %s", reply.Error)
	}
	return reply.Text, nil
}

// idleOut ends w when it is still the worker and has answered no dictation
// since its uses-th.
func (v *Voice) idleOut(w *worker, uses int) {
	select {
	case <-v.turn:
	case <-v.closed:
		return
	}
	defer v.give()
	if v.worker == w && w.uses == uses {
		v.retire()
	}
}

type workerAnswer struct {
	workerReply
	err error
}

// ask sends one sound and reads the reply.
func (w *worker) ask(sound []byte) workerAnswer {
	if err := writeFrame(w.input, sound); err != nil {
		return workerAnswer{err: err}
	}
	data, err := readFrame(w.output, MAX_REPLY_BYTES)
	if err != nil {
		return workerAnswer{err: err}
	}
	var reply workerReply
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return workerAnswer{err: fmt.Errorf("its reply could not be read: %w", err)}
	}
	return workerAnswer{workerReply: reply}
}

func readFrame(input io.Reader, maximum uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > maximum {
		return nil, fmt.Errorf("a frame of %d bytes is not between 1 and %d", size, maximum)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(input, data); err != nil {
		return nil, fmt.Errorf("a frame ended early: %w", err)
	}
	return data, nil
}

func writeFrame(output io.Writer, data []byte) error {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := output.Write(header[:]); err != nil {
		return err
	}
	_, err := output.Write(data)
	return err
}

// RunWorker is the worker's side: it recognises each sound input brings and
// answers on output, until input closes.
func RunWorker(input io.Reader, output io.Writer, recognize func([]byte) (string, error)) error {
	for {
		sound, err := readFrame(input, MAX_SOUND_BYTES)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var reply workerReply
		if text, err := recognize(sound); err != nil {
			reply.Error = err.Error()
		} else {
			reply.Text = strings.TrimSpace(text)
		}
		data, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		if err := writeFrame(output, data); err != nil {
			return err
		}
	}
}
