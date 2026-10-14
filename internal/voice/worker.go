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
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const (
	WORKER_IDLE       = 120 * time.Second
	MAX_SOUND_BYTES   = 8 << 20
	MAX_REPLY_BYTES   = 1 << 20
	MAX_WORKER_STDERR = 4096
)

type workerReply struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

type worker struct {
	command     *exec.Cmd
	input       io.WriteCloser
	output      io.ReadCloser
	exited      chan struct{}
	idle        *time.Timer
	isActive    bool
	shutdownErr error
	logMutex    sync.Mutex
	log         []byte
}

func (w *worker) Write(data []byte) (int, error) {
	w.logMutex.Lock()
	defer w.logMutex.Unlock()
	w.log = append(w.log, data...)
	if len(w.log) > MAX_WORKER_STDERR {
		w.log = append([]byte(nil), w.log[len(w.log)-MAX_WORKER_STDERR:]...)
	}
	return len(data), nil
}

func (w *worker) stop() error {
	if w.idle != nil {
		w.idle.Stop()
	}
	_ = w.input.Close()
	_ = w.output.Close()
	// Windows Wait releases the handle before exited closes; only raw EINVAL means already waited.
	if err := killWorker(w.command.Process); err != nil && !errors.Is(err, os.ErrProcessDone) && (runtime.GOOS != "windows" || err != syscall.EINVAL) {
		w.shutdownErr = fmt.Errorf("stop the owned dictation worker: %w", err)
		return w.shutdownErr
	}
	<-w.exited
	return nil
}

func (v *Voice) initialize() {
	v.once.Do(func() {
		v.turn = make(chan struct{}, 1)
		v.turn <- struct{}{}
		v.closed = make(chan struct{})
	})
}

func (v *Voice) acquire(ctx context.Context) error {
	v.initialize()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.closed:
		return errors.New("dictation is closed")
	case <-v.turn:
	}
	v.mutex.Lock()
	isClosed := v.isClosed
	v.mutex.Unlock()
	if err := ctx.Err(); err != nil {
		v.turn <- struct{}{}
		return err
	}
	if isClosed {
		v.turn <- struct{}{}
		return errors.New("dictation is closed")
	}
	return nil
}

func (v *Voice) retire(expected *worker) error {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if v.worker == nil || expected != nil && v.worker != expected {
		return nil
	}
	if err := v.worker.stop(); err != nil {
		return err
	}
	v.worker = nil
	return nil
}

// Close drops pending words and reaps only this Voice's worker.
func (v *Voice) Close() error {
	v.initialize()
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if !v.isClosed {
		v.isClosed = true
		close(v.closed)
	}
	if v.worker == nil {
		return nil
	}
	if err := v.worker.stop(); err != nil {
		return err
	}
	v.worker = nil
	return nil
}

func (v *Voice) startWorker() (*worker, error) {
	program, err := os.Executable()
	if err != nil {
		return nil, err
	}
	arguments := []string{"voice-worker", filepath.Join(v.folder(v.Settings.Engine), v.Settings.Program)}
	for _, argument := range v.Settings.Args {
		arguments = append(arguments, strings.ReplaceAll(argument, "{model}", v.folder(v.Settings.Model)))
	}
	command := execx.Command(program, arguments...)
	command.Dir = v.folder(v.Settings.Engine)
	command.Env = []string{}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "PATHEXT", "TEMP", "TMP"} {
		if value, ok := os.LookupEnv(name); ok {
			command.Env = append(command.Env, name+"="+value)
		}
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	w := &worker{command: command, input: input, output: output, exited: make(chan struct{})}
	command.Stderr = w
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	go func() {
		_ = command.Wait()
		close(w.exited)
	}()
	return w, nil
}

func (v *Voice) exchange(ctx context.Context, sound []byte) (string, error) {
	if len(sound) == 0 || len(sound) > MAX_SOUND_BYTES {
		return "", fmt.Errorf("dictation sound must contain 1 to %d bytes", MAX_SOUND_BYTES)
	}
	v.mutex.Lock()
	if v.isClosed {
		v.mutex.Unlock()
		return "", errors.New("dictation is closed")
	}
	if v.worker == nil {
		var err error
		v.worker, err = v.startWorker()
		if err != nil {
			v.mutex.Unlock()
			return "", fmt.Errorf("start the dictation worker: %w", err)
		}
	}
	w := v.worker
	if w.shutdownErr != nil {
		v.mutex.Unlock()
		return "", w.shutdownErr
	}
	if w.idle != nil {
		w.idle.Stop()
	}
	w.isActive = true
	v.mutex.Unlock()
	result := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		err := writeFrame(w.input, sound)
		var data []byte
		if err == nil {
			data, err = readFrame(w.output, MAX_REPLY_BYTES)
		}
		result <- struct {
			data []byte
			err  error
		}{data, err}
	}()
	var data []byte
	select {
	case <-ctx.Done():
		err := v.retire(w)
		<-result
		return "", errors.Join(ctx.Err(), err)
	case answer := <-result:
		if answer.err != nil {
			stopErr := v.retire(w)
			w.logMutex.Lock()
			log := lastLines(w.log, 3)
			w.logMutex.Unlock()
			return "", errors.Join(fmt.Errorf("the dictation engine failed: %w: %s", answer.err, log), stopErr)
		}
		data = answer.data
	}
	if err := ctx.Err(); err != nil {
		return "", errors.Join(err, v.retire(w))
	}
	var reply workerReply
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return "", errors.Join(fmt.Errorf("read dictation worker reply: %w", err), v.retire(w))
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || reply.Error != "" && reply.Text != "" {
		return "", errors.Join(errors.New("the dictation worker sent an invalid reply"), v.retire(w))
	}
	v.mutex.Lock()
	if v.isClosed {
		v.mutex.Unlock()
		return "", errors.New("dictation is closed")
	}
	if err := ctx.Err(); err != nil {
		v.mutex.Unlock()
		return "", errors.Join(err, v.retire(w))
	}
	defer v.mutex.Unlock()
	w.isActive = false
	var idle *time.Timer
	idle = time.AfterFunc(WORKER_IDLE, func() {
		v.mutex.Lock()
		defer v.mutex.Unlock()
		if v.worker == w && w.idle == idle && !w.isActive {
			if err := w.stop(); err == nil {
				v.worker = nil
			}
		}
	})
	w.idle = idle
	if reply.Error != "" {
		return "", fmt.Errorf("the dictation engine failed: %s", reply.Error)
	}
	return strings.TrimSpace(reply.Text), nil
}

func readFrame(input io.Reader, maximum uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(input, header[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size == 0 || size > maximum {
		return nil, fmt.Errorf("dictation frame size %d is outside 1 to %d", size, maximum)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(input, data); err != nil {
		return nil, fmt.Errorf("read dictation frame payload: %w", err)
	}
	return data, nil
}

func writeFrame(output io.Writer, data []byte) error {
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := io.Copy(output, bytes.NewReader(header[:])); err != nil {
		return err
	}
	_, err := io.Copy(output, bytes.NewReader(data))
	return err
}

// RunWorker serves one sound at a time over private standard-input/output pipes.
func RunWorker(input io.Reader, output io.Writer, recognize func([]byte) (string, error)) error {
	for {
		sound, err := readFrame(input, MAX_SOUND_BYTES)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		text, err := recognize(sound)
		reply := workerReply{Text: strings.TrimSpace(text)}
		if err != nil {
			reply.Text, reply.Error = "", err.Error()
		}
		data, err := json.Marshal(reply)
		if err != nil {
			return err
		}
		if len(data) > MAX_REPLY_BYTES {
			return errors.New("dictation worker reply exceeds its limit")
		}
		if err := writeFrame(output, data); err != nil {
			return err
		}
	}
}
