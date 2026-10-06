package voice

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsSherpaAPI calls the engine's library. What the library returns by
// pointer is read with ReadProcessMemory, so no address it hands back is
// made into a Go pointer.
type windowsSherpaAPI struct {
	createRecognizerProc *windows.Proc
	readWaveProc         *windows.Proc
	freeWaveProc         *windows.Proc
	createStreamProc     *windows.Proc
	destroyStreamProc    *windows.Proc
	acceptWaveformProc   *windows.Proc
	decodeProc           *windows.Proc
	resultJSONProc       *windows.Proc
	freeJSONProc         *windows.Proc
}

// OpenWorker loads the engine's library and the model, and returns what
// recognises a sound with them. The library's own dependencies are looked
// for beside it and in System32 only.
func OpenWorker(options WorkerOptions) (func([]byte) (string, error), error) {
	handle, err := windows.LoadLibraryEx(options.Library, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("load the engine %s: %w", options.Library, err)
	}
	library := &windows.DLL{Name: options.Library, Handle: handle}
	api := &windowsSherpaAPI{}
	for name, target := range map[string]**windows.Proc{
		"SherpaOnnxCreateOfflineRecognizer":        &api.createRecognizerProc,
		"SherpaOnnxReadWaveFromBinaryData":         &api.readWaveProc,
		"SherpaOnnxFreeWave":                       &api.freeWaveProc,
		"SherpaOnnxCreateOfflineStream":            &api.createStreamProc,
		"SherpaOnnxDestroyOfflineStream":           &api.destroyStreamProc,
		"SherpaOnnxAcceptWaveformOffline":          &api.acceptWaveformProc,
		"SherpaOnnxDecodeOfflineStream":            &api.decodeProc,
		"SherpaOnnxGetOfflineStreamResultAsJson":   &api.resultJSONProc,
		"SherpaOnnxDestroyOfflineStreamResultJson": &api.freeJSONProc,
	} {
		if *target, err = library.FindProc(name); err != nil {
			return nil, fmt.Errorf("the engine %s has no %s: %w", options.Library, name, err)
		}
	}
	recognizer, err := newSherpaRecognizer(options, api)
	if err != nil {
		return nil, err
	}
	return recognizer.Recognize, nil
}

func (api *windowsSherpaAPI) createRecognizer(config *sherpaOfflineRecognizerConfig) uintptr {
	result, _, _ := api.createRecognizerProc.Call(uintptr(unsafe.Pointer(config)))
	runtime.KeepAlive(config)
	return result
}

func (api *windowsSherpaAPI) readWave(sound []byte) uintptr {
	result, _, _ := api.readWaveProc.Call(uintptr(unsafe.Pointer(&sound[0])), uintptr(len(sound)))
	runtime.KeepAlive(sound)
	return result
}

// waveData reads the SherpaOnnxWave at handle: the samples' address, the
// sample rate and the number of samples.
func (api *windowsSherpaAPI) waveData(handle uintptr) (sherpaWave, error) {
	var data [16]byte
	var read uintptr
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), handle, &data[0], uintptr(len(data)), &read); err != nil {
		return sherpaWave{}, err
	}
	if read != uintptr(len(data)) {
		return sherpaWave{}, errors.New("the decoded sound could not be read in full")
	}
	return sherpaWave{
		Samples:    uintptr(binary.LittleEndian.Uint64(data[:8])),
		SampleRate: int32(binary.LittleEndian.Uint32(data[8:12])),
		NumSamples: int32(binary.LittleEndian.Uint32(data[12:])),
	}, nil
}

func (api *windowsSherpaAPI) freeWave(handle uintptr) {
	_, _, _ = api.freeWaveProc.Call(handle)
}

func (api *windowsSherpaAPI) createStream(recognizer uintptr) uintptr {
	result, _, _ := api.createStreamProc.Call(recognizer)
	return result
}

func (api *windowsSherpaAPI) destroyStream(handle uintptr) {
	_, _, _ = api.destroyStreamProc.Call(handle)
}

func (api *windowsSherpaAPI) acceptWaveform(stream uintptr, wave sherpaWave) {
	_, _, _ = api.acceptWaveformProc.Call(stream, uintptr(wave.SampleRate), wave.Samples, uintptr(wave.NumSamples))
}

func (api *windowsSherpaAPI) decode(recognizer, stream uintptr) {
	_, _, _ = api.decodeProc.Call(recognizer, stream)
}

func (api *windowsSherpaAPI) resultJSON(stream uintptr) uintptr {
	result, _, _ := api.resultJSONProc.Call(stream)
	return result
}

// readJSON reads the NUL-terminated text at handle, a byte at a time so that
// no read crosses past its end.
func (api *windowsSherpaAPI) readJSON(handle uintptr) ([]byte, error) {
	var data []byte
	for offset := uintptr(0); offset < MAX_REPLY_BYTES; offset++ {
		var value byte
		if err := windows.ReadProcessMemory(windows.CurrentProcess(), handle+offset, &value, 1, nil); err != nil {
			return nil, err
		}
		if value == 0 {
			return data, nil
		}
		data = append(data, value)
	}
	return nil, fmt.Errorf("the engine's result is longer than %d bytes", MAX_REPLY_BYTES)
}

func (api *windowsSherpaAPI) freeJSON(handle uintptr) {
	_, _, _ = api.freeJSONProc.Call(handle)
}
