package main

import (
	"path/filepath"
	"testing"
)

func TestDeviceTypePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "types.db")
	db, e := newSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	db.RegisterDevice("sim", "", "simulator")
	restored, e := newSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	d := restored.ListDevices()
	if len(d) != 1 || d[0].DeviceType != "simulator" {
		t.Fatalf("type not restored: %+v", d)
	}
}
