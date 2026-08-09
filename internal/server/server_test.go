package server

import "testing"

func TestProjectionFields(t *testing.T) {
	fields, err := projectionFields("id, name,id")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields[0] != "id" || fields[1] != "name" {
		t.Fatalf("unexpected fields: %#v", fields)
	}
	if _, err = projectionFields("id,password.hash"); err == nil {
		t.Fatal("expected an invalid field error")
	}
}

func TestProjectObject(t *testing.T) {
	projected := projectObject(map[string]any{"id": "1", "name": "Relio", "secret": "hidden"}, []string{"id", "name"})
	if len(projected) != 2 || projected["id"] != "1" || projected["name"] != "Relio" {
		t.Fatalf("unexpected projection: %#v", projected)
	}
	if _, ok := projected["secret"]; ok {
		t.Fatal("unselected field leaked into projection")
	}
}
