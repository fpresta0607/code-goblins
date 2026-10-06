package disk

import "testing"

func TestReadReadsTheDriveHoldingAFolder(t *testing.T) {
	reading, err := Read(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if reading.Total == 0 || reading.Free == 0 || reading.Free > reading.Total || reading.Drive == "" {
		t.Errorf("reading = %+v, want a named drive with some space of its size free", reading)
	}
}
