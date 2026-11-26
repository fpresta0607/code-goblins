package processor

import (
	"fmt"
	"math/bits"
	"reflect"
	"testing"
	"testing/fstest"

	"golang.org/x/sys/unix"
)

// sysfs is a sysfs that names the online threads online and, for each thread
// in core, the threads of its core.
func sysfs(online string, core map[int]string) fstest.MapFS {
	sys := fstest.MapFS{"devices/system/cpu/online": {Data: []byte(online + "\n")}}
	for thread, siblings := range core {
		sys[fmt.Sprintf("devices/system/cpu/cpu%d/topology/core_cpus_list", thread)] = &fstest.MapFile{Data: []byte(siblings + "\n")}
	}
	return sys
}

// with adds the files of more to sys.
func with(sys fstest.MapFS, more map[string]string) fstest.MapFS {
	for path, text := range more {
		sys[path] = &fstest.MapFile{Data: []byte(text + "\n")}
	}
	return sys
}

func TestReadCoresReadsEachCoresKindAndThreadsFromSys(t *testing.T) {
	// An Intel hybrid PC of six performance cores of two threads and four
	// efficiency cores of one, numbered as Linux numbers them.
	hybrid := sysfs("0-15", map[int]string{
		0: "0-1", 1: "0-1", 2: "2-3", 3: "2-3", 4: "4-5", 5: "4-5", 6: "6-7", 7: "6-7", 8: "8-9", 9: "8-9", 10: "10-11", 11: "10-11",
		12: "12", 13: "13", 14: "14", 15: "15",
	})
	with(hybrid, map[string]string{"devices/cpu_core/cpus": "0-11", "devices/cpu_atom/cpus": "12-15"})
	// An ARM machine of two big cores, 1024, and four little ones, 446, of
	// one thread each, the little ones numbered first.
	bigLittle := sysfs("0-5", map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4", 5: "5"})
	for thread := range 6 {
		capacity := "446"
		if thread >= 4 {
			capacity = "1024"
		}
		with(bigLittle, map[string]string{fmt.Sprintf("devices/system/cpu/cpu%d/cpu_capacity", thread): capacity})
	}
	// An ARM machine of three kinds: one prime core, two big, two little.
	threeKinds := sysfs("0-4", map[int]string{0: "0", 1: "1", 2: "2", 3: "3", 4: "4"})
	for thread, capacity := range []string{"325", "325", "870", "870", "1024"} {
		with(threeKinds, map[string]string{fmt.Sprintf("devices/system/cpu/cpu%d/cpu_capacity", thread): capacity})
	}
	for _, test := range []struct {
		name string
		sys  fstest.MapFS
		want []Core
	}{
		{
			"an Intel hybrid PC, its performance cores the ones the core counter unit names",
			hybrid,
			[]Core{{1, []int{0, 1}}, {1, []int{2, 3}}, {1, []int{4, 5}}, {1, []int{6, 7}}, {1, []int{8, 9}}, {1, []int{10, 11}}, {0, []int{12}}, {0, []int{13}}, {0, []int{14}}, {0, []int{15}}},
		},
		{"an ARM machine of big and little cores, by capacity", bigLittle, []Core{{0, []int{0}}, {0, []int{1}}, {0, []int{2}}, {0, []int{3}}, {1, []int{4}}, {1, []int{5}}}},
		{"an ARM machine of three kinds, ranked by capacity", threeKinds, []Core{{0, []int{0}}, {0, []int{1}}, {1, []int{2}}, {1, []int{3}}, {2, []int{4}}}},
		{
			"a machine of one kind, its two threads a core numbered half the machine apart",
			sysfs("0-3", map[int]string{0: "0,2", 1: "1,3", 2: "0,2", 3: "1,3"}),
			[]Core{{0, []int{0, 2}}, {0, []int{1, 3}}},
		},
		{
			"a machine with a thread taken offline, which no core names",
			sysfs("0-2", map[int]string{0: "0-1", 1: "0-1", 2: "2-3"}),
			[]Core{{0, []int{0, 1}}, {0, []int{2}}},
		},
		{
			"a machine with one thread's capacity missing, read as one kind",
			with(sysfs("0-1", map[int]string{0: "0", 1: "1"}), map[string]string{"devices/system/cpu/cpu1/cpu_capacity": "1024"}),
			[]Core{{0, []int{0}}, {0, []int{1}}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			got, err := readCores(test.sys)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("the cores read %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestReadCoresRefusesASysItCannotRead(t *testing.T) {
	for _, test := range []struct {
		name string
		sys  fstest.MapFS
	}{
		{"no online threads named", fstest.MapFS{}},
		{"no online threads", sysfs("", nil)},
		{"a list that is no list", sysfs("0-x", map[int]string{0: "0"})},
		{"a range that runs backwards", sysfs("3-0", map[int]string{0: "0"})},
		{"a thread whose core is not named", sysfs("0-1", map[int]string{0: "0"})},
		{"a capacity that is no number", with(sysfs("0", map[int]string{0: "0"}), map[string]string{"devices/system/cpu/cpu0/cpu_capacity": "fast"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			cores, err := readCores(test.sys)

			// Assert
			if err == nil {
				t.Fatalf("readCores read %+v, want an error", cores)
			}
		})
	}
}

// ownThreads is the mask of the processor threads this test may run on, of
// a machine of no more threads than a mask names.
func ownThreads(t *testing.T) uintptr {
	t.Helper()
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &set); err != nil {
		t.Fatal(err)
	}
	var own uintptr
	for thread := range 1024 {
		if set.IsSet(thread) {
			if thread >= bits.UintSize {
				t.Skipf("this test may run on thread %d, which a mask does not name", thread)
			}
			own |= 1 << thread
		}
	}
	return own
}

func TestMachineCoresNamesEachThreadThisProcessMayRunOnOnce(t *testing.T) {
	// Arrange
	own := ownThreads(t)

	// Act
	cores, err := MachineCores()

	// Assert
	t.Logf("this machine's cores: %+v", cores)
	if err != nil {
		t.Fatal(err)
	}
	var named uintptr
	for _, core := range cores {
		for _, thread := range core.Threads {
			if named&(1<<thread) != 0 {
				t.Fatalf("thread %d is named by two cores of %+v", thread, cores)
			}
			named |= 1 << thread
		}
	}
	if own&^named != 0 {
		t.Fatalf("the cores name the threads %#x, without %#x, which this test may run on", named, own&^named)
	}
}
