package voice

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsSherpaAPI struct {
	library               *windows.DLL
	createRecognizerProc  *windows.Proc
	destroyRecognizerProc *windows.Proc
	readWaveProc          *windows.Proc
	freeWaveProc          *windows.Proc
	createStreamProc      *windows.Proc
	destroyStreamProc     *windows.Proc
	acceptWaveformProc    *windows.Proc
	decodeProc            *windows.Proc
	resultJSONProc        *windows.Proc
	freeJSONProc          *windows.Proc
}

func OpenWorker(options WorkerOptions) (WorkerRecognizer, error) {
	if err := checkSherpaLayout(); err != nil {
		return nil, err
	}
	for _, path := range []string{options.Library, options.Encoder, options.Decoder, options.Joiner, options.Tokens} {
		file, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("the pinned dictation file %s is unavailable: %w", path, err)
		}
		if !file.Mode().IsRegular() {
			return nil, fmt.Errorf("the pinned dictation path %s is not a file", path)
		}
	}
	program, err := pe.Open(options.Library)
	if err != nil {
		return nil, fmt.Errorf("read the pinned dictation C API: %w", err)
	}
	isX64 := program.Machine == pe.IMAGE_FILE_MACHINE_AMD64 && program.Characteristics&pe.IMAGE_FILE_DLL != 0
	_, isPE32Plus := program.OptionalHeader.(*pe.OptionalHeader64)
	closeErr := program.Close()
	if closeErr != nil || !isX64 || !isPE32Plus {
		return nil, errors.Join(errors.New("the dictation C API must be a Windows x64 PE32+ library"), closeErr)
	}
	if err := offline(filepath.Dir(options.Library)); err != nil {
		return nil, err
	}
	handle, err := windows.LoadLibraryEx(options.Library, 0, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
	if err != nil {
		return nil, fmt.Errorf("load the pinned dictation C API: %w", err)
	}
	api := &windowsSherpaAPI{library: &windows.DLL{Name: options.Library, Handle: handle}}
	procedures := []struct {
		name   string
		target **windows.Proc
	}{
		{"SherpaOnnxCreateOfflineRecognizer", &api.createRecognizerProc},
		{"SherpaOnnxDestroyOfflineRecognizer", &api.destroyRecognizerProc},
		{"SherpaOnnxReadWaveFromBinaryData", &api.readWaveProc},
		{"SherpaOnnxFreeWave", &api.freeWaveProc},
		{"SherpaOnnxCreateOfflineStream", &api.createStreamProc},
		{"SherpaOnnxDestroyOfflineStream", &api.destroyStreamProc},
		{"SherpaOnnxAcceptWaveformOffline", &api.acceptWaveformProc},
		{"SherpaOnnxDecodeOfflineStream", &api.decodeProc},
		{"SherpaOnnxGetOfflineStreamResultAsJson", &api.resultJSONProc},
		{"SherpaOnnxDestroyOfflineStreamResultJson", &api.freeJSONProc},
	}
	for _, procedure := range procedures {
		*procedure.target, err = api.library.FindProc(procedure.name)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("the pinned C API is missing %s: %w", procedure.name, err), api.release())
		}
	}
	return newSherpaRecognizer(options, api)
}

func checkSherpaLayout() error {
	if unsafe.Sizeof(sherpaFeatureConfig{}) != 8 || unsafe.Sizeof(sherpaOfflineModelConfig{}) != 504 || unsafe.Sizeof(sherpaOfflineRecognizerConfig{}) != 608 || unsafe.Sizeof(sherpaWave{}) != 16 {
		return errors.New("the dictation C API declaration does not match the pinned x64 layout")
	}
	return nil
}

func (api *windowsSherpaAPI) createRecognizer(config *sherpaOfflineRecognizerConfig) uintptr {
	result, _, _ := api.createRecognizerProc.Call(uintptr(unsafe.Pointer(config)))
	runtime.KeepAlive(config)
	return result
}

func (api *windowsSherpaAPI) destroyRecognizer(handle uintptr) {
	_, _, _ = api.destroyRecognizerProc.Call(handle)
}

func (api *windowsSherpaAPI) readWave(sound []byte) uintptr {
	result, _, _ := api.readWaveProc.Call(uintptr(unsafe.Pointer(&sound[0])), uintptr(len(sound)))
	runtime.KeepAlive(sound)
	return result
}

func (api *windowsSherpaAPI) waveData(handle uintptr) (sherpaWave, error) {
	var data [16]byte
	var read uintptr
	if err := windows.ReadProcessMemory(windows.CurrentProcess(), handle, &data[0], uintptr(len(data)), &read); err != nil {
		return sherpaWave{}, err
	}
	if read != uintptr(len(data)) {
		return sherpaWave{}, errors.New("the C API wave could not be read in full")
	}
	return sherpaWave{
		Samples: uintptr(binary.LittleEndian.Uint64(data[:8])),
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

func (api *windowsSherpaAPI) readJSON(handle uintptr) ([]byte, error) {
	var data []byte
	for index := uintptr(0); index < MAX_REPLY_BYTES; index++ {
		var value byte
		var read uintptr
		if err := windows.ReadProcessMemory(windows.CurrentProcess(), handle+index, &value, 1, &read); err != nil {
			return nil, err
		}
		if read != 1 {
			return nil, errors.New("the C API result could not be read in full")
		}
		if value == 0 {
			return data, nil
		}
		data = append(data, value)
	}
	return nil, errors.New("the C API result exceeds the dictation reply limit")
}

func (api *windowsSherpaAPI) freeJSON(handle uintptr) {
	_, _, _ = api.freeJSONProc.Call(handle)
}

func (api *windowsSherpaAPI) release() error {
	if api.library == nil {
		return nil
	}
	if err := api.library.Release(); err != nil {
		return err
	}
	api.library = nil
	return nil
}
