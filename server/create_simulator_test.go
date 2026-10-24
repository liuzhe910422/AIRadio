package main

import (
	"path/filepath"
	"testing"
)

func TestCreateSimulatorOffline(t *testing.T) {
	for _, persist := range []bool{false, true} {
		s := newServer()
		path := filepath.Join(t.TempDir(), "sim.db")
		if persist {
			db, e := newSQLiteStore(path)
			if e != nil {
				t.Fatal(e)
			}
			s.store = db
		}
		if e := s.store.CreateSimulator("008"); e != nil {
			t.Fatal(e)
		}
		if e := s.store.CreateSimulator("008"); e == nil {
			t.Fatal("duplicate accepted")
		}
		if persist {
			db, e := newSQLiteStore(path)
			if e != nil {
				t.Fatal(e)
			}
			s.store = db
		}
		ds := s.store.ListDevices()
		if len(ds) != 1 || ds[0].ID != "008" || ds[0].Online || ds[0].LastSeen != "" || ds[0].DeviceType != "simulator" || ds[0].RoomID != "default" {
			t.Fatalf("wrong offline device: %+v", ds)
		}
	}
}
