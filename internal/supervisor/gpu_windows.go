package supervisor

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	pdh                         = windows.NewLazySystemDLL("pdh.dll")
	pdhOpenQuery                = pdh.NewProc("PdhOpenQueryW")
	pdhAddEnglishCounter        = pdh.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData         = pdh.NewProc("PdhCollectQueryData")
	pdhGetFormattedCounterArray = pdh.NewProc("PdhGetFormattedCounterArrayW")
)

const (
	// gpuEngineCounter is every program's use of every engine of every
	// adapter, in percent, by its English name whatever Windows' language.
	gpuEngineCounter = `\GPU Engine(*)\Utilization Percentage`
	pdhFormatDouble  = 0x00000200
	pdhMoreData      = 0x800007D2
	// softwareVendor and softwareDevice are Windows' own software renderer,
	// which draws on the processors and is no graphics adapter.
	softwareVendor = 0x1414
	softwareDevice = 0x8c
)

// pdhItem is one PDH_FMT_COUNTERVALUE_ITEM_W holding a double.
type pdhItem struct {
	name   *uint16
	status uint32
	_      uint32
	value  float64
}

// gpuMeter keeps Windows' query of the engines open between readings, since
// each reading is the use since the one before it, and when the last was
// taken: the zero time before any.
type gpuMeter struct {
	mu      sync.Mutex
	query   uintptr
	counter uintptr
	at      time.Time
	last    GPU
}

var machineGPU gpuMeter

// MachineGPU reads this machine's graphics adapters and how busy each was
// since the last reading, or over a moment when there is no reading recent
// enough (howToReadProcessors).
func MachineGPU() (GPU, error) {
	return machineGPU.read()
}

func (m *gpuMeter) read() (GPU, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.query == 0 {
		var query, counter uintptr
		if status, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&query))); status != 0 {
			return GPU{}, fmt.Errorf("open the query of the graphics engines: status %#x", status)
		}
		path, err := windows.UTF16PtrFromString(gpuEngineCounter)
		if err != nil {
			return GPU{}, err
		}
		if status, _, _ := pdhAddEnglishCounter.Call(query, uintptr(unsafe.Pointer(path)), 0, uintptr(unsafe.Pointer(&counter))); status != 0 {
			return GPU{}, fmt.Errorf("%w (status %#x)", ErrNoGPU, status)
		}
		m.query, m.counter = query, counter
	}
	switch howToReadProcessors(!m.at.IsZero(), time.Since(m.at)) {
	case repeatLast:
		return m.last, nil
	case watchAMoment:
		// A machine with no engine in use yet has nothing to collect, which
		// the second collection below reports.
		_, _, _ = pdhCollectQueryData.Call(m.query)
		time.Sleep(processorMoment)
	}
	if status, _, _ := pdhCollectQueryData.Call(m.query); status != 0 {
		return GPU{}, fmt.Errorf("%w (collecting: status %#x)", ErrNoGPU, status)
	}
	uses, err := m.uses()
	if err != nil {
		return GPU{}, err
	}
	adapters, err := machineGPUAdapters()
	if err != nil {
		return GPU{}, err
	}
	// The app a program belongs to names it, as for the commit holders: a
	// hidden window's renderer counts as the app whose window it is.
	names := map[uint32]string{}
	if processes, err := machineProcessList(); err == nil {
		app := appNames(processes)
		for _, process := range processes {
			names[process.pid] = app(process)
		}
	}
	gpu, err := readGPU(adapters, uses, func(pid uint32) string { return names[pid] })
	if err != nil {
		return GPU{}, err
	}
	m.at, m.last = time.Now(), gpu
	return gpu, nil
}

// uses reads every program's use of every engine from the last collection.
func (m *gpuMeter) uses() ([]gpuEngineUse, error) {
	var size, count uint32
	status, _, _ := pdhGetFormattedCounterArray.Call(m.counter, pdhFormatDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if status == 0 || size == 0 {
		return nil, nil
	}
	if status != pdhMoreData {
		return nil, fmt.Errorf("size the use of the graphics engines: status %#x", status)
	}
	buffer := make([]byte, size)
	if status, _, _ := pdhGetFormattedCounterArray.Call(m.counter, pdhFormatDouble, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buffer[0]))); status != 0 {
		return nil, fmt.Errorf("read the use of the graphics engines: status %#x", status)
	}
	items := unsafe.Slice((*pdhItem)(unsafe.Pointer(&buffer[0])), count)
	uses := make([]gpuEngineUse, 0, count)
	for _, item := range items {
		// 0 is valid data and 1 new data; anything else is no reading.
		if item.status > 1 || item.name == nil {
			continue
		}
		uses = append(uses, gpuEngineUse{instance: windows.UTF16PtrToString(item.name), percent: item.value})
	}
	return uses, nil
}

// machineGPUAdapters lists the graphics adapters Windows saw at this start
// of the machine, by the number their engines' counters carry: Windows keeps
// one record for each under its DirectX key, stamped with when it last saw
// it, and the key itself carries the stamp of this start.
func machineGPUAdapters() ([]gpuAdapter, error) {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\DirectX`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("read the graphics adapters: %w", err)
	}
	defer root.Close()
	seen, _, err := root.GetIntegerValue("LastSeen")
	if err != nil {
		return nil, fmt.Errorf("read when Windows last saw its graphics adapters: %w", err)
	}
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("list the graphics adapters: %w", err)
	}
	var adapters []gpuAdapter
	for _, name := range names {
		key, err := registry.OpenKey(root, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		luid, _, luidErr := key.GetIntegerValue("AdapterLuid")
		description, _, descriptionErr := key.GetStringValue("Description")
		last, _, lastErr := key.GetIntegerValue("LastSeen")
		vendor, _, _ := key.GetIntegerValue("VendorId")
		device, _, _ := key.GetIntegerValue("DeviceId")
		key.Close()
		if luidErr != nil || descriptionErr != nil || lastErr != nil || last != seen || vendor == softwareVendor && device == softwareDevice {
			continue
		}
		adapters = append(adapters, gpuAdapter{luid: luid, name: description})
	}
	return adapters, nil
}
