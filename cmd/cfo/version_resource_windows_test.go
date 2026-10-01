package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Every build of cfo, a test binary included, links the package's resource
// object, so this binary shows what a release or a deployment build carries:
// a version resource that names the product, and a manifest that asks for no
// elevation.
func TestEveryBuildCarriesAVersionResourceAndAnAsInvokerManifest(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	size, err := windows.GetFileVersionInfoSize(executable, nil)
	if err != nil {
		t.Fatalf("%s has no version resource: %v", executable, err)
	}
	info := make([]byte, size)
	if err := windows.GetFileVersionInfo(executable, 0, size, unsafe.Pointer(&info[0])); err != nil {
		t.Fatal(err)
	}
	if got := versionString(t, info, "ProductName"); got != "Code Goblins" {
		t.Errorf("ProductName is %q, want Code Goblins", got)
	}
	if got := versionString(t, info, "FileDescription"); got == "" {
		t.Error("FileDescription is empty")
	}
	// goblins.exe is this same file under a second name, and a file whose
	// OriginalFilename differs from the name it runs as reads as a renamed
	// tool.
	if got := versionString(t, info, "OriginalFilename"); got != "" {
		t.Errorf("OriginalFilename is %q, want none", got)
	}

	module, err := windows.LoadLibraryEx(executable, 0, windows.LOAD_LIBRARY_AS_DATAFILE)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.FreeLibrary(module)
	resource, err := windows.FindResource(module, windows.ResourceID(1), windows.RT_MANIFEST)
	if err != nil {
		t.Fatalf("%s has no application manifest: %v", executable, err)
	}
	manifest, err := windows.LoadResourceData(module, resource)
	if err != nil {
		t.Fatal(err)
	}
	var assembly struct {
		RequestedExecutionLevel struct {
			Level    string `xml:"level,attr"`
			UIAccess string `xml:"uiAccess,attr"`
		} `xml:"trustInfo>security>requestedPrivileges>requestedExecutionLevel"`
	}
	if err := xml.Unmarshal(manifest, &assembly); err != nil {
		t.Fatalf("the manifest is not XML: %v\n%s", err, manifest)
	}
	if got := assembly.RequestedExecutionLevel.Level; got != "asInvoker" {
		t.Errorf("the manifest requests execution level %q, want asInvoker:\n%s", got, manifest)
	}
	if got := assembly.RequestedExecutionLevel.UIAccess; got != "false" {
		t.Errorf("the manifest sets uiAccess %q, want false:\n%s", got, manifest)
	}
}

// versionString reads one string of the version resource in its first
// language, or "" when it has none.
func versionString(t *testing.T, info []byte, name string) string {
	t.Helper()
	var translation *[2]uint16
	var length uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&info[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&translation), &length); err != nil || length < 4 {
		t.Fatalf("the version resource has no translation: %v", err)
	}
	var value *uint16
	path := fmt.Sprintf(`\StringFileInfo\%04x%04x\%s`, translation[0], translation[1], name)
	if err := windows.VerQueryValue(unsafe.Pointer(&info[0]), path, unsafe.Pointer(&value), &length); err != nil || length == 0 {
		return ""
	}
	return windows.UTF16PtrToString(value)
}
