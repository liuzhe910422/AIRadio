package main

import (
	"path/filepath"
	"testing"
)

func TestDeleteSimulatorPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delete.db")
	db, e := newSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	db.RegisterDevice("004", "", "simulator")
	db.RegisterDevice("002", "", "physical")
	r, e := db.CreateRoom("444", "004", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.DeleteSimulator("002"); e == nil {
		t.Fatal("physical device deleted")
	}
	if e = db.DeleteSimulator("004"); e != nil {
		t.Fatal(e)
	}
	if len(db.RoomDevices(r.ID)) != 0 {
		t.Fatal("membership retained")
	}
	restored, e := newSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range restored.ListDevices() {
		if d.ID == "004" {
			t.Fatal("deleted device restored")
		}
	}
}
