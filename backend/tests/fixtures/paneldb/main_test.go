package main

import (
	"database/sql"
	"strings"
	"testing"
)

func TestTrafficFixtureIdentityAndAtomicity(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		`CREATE TABLE clients(email TEXT,uuid TEXT,sub_id TEXT,enable INTEGER)`,
		`CREATE TABLE client_traffics(email TEXT,up INTEGER,down INTEGER)`,
		`INSERT INTO clients VALUES ('owned','vpn','sub',0),('other','vpn2','sub2',1)`,
		`INSERT INTO client_traffics VALUES ('owned',10,20),('owned',30,40),('other',50,60)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	in := trafficInput{PanelKey: "owned", VPNID: "vpn", SubID: "sub", Up: 111, Down: 222, Memberships: 2, RequireDisabled: true}
	wrong := in
	wrong.VPNID = "changed"
	if setTraffic(db, wrong) == nil {
		t.Fatal("identity mismatch accepted")
	}
	wrong = in
	wrong.Memberships = 3
	if setTraffic(db, wrong) == nil {
		t.Fatal("membership mismatch accepted")
	}
	var up, down int
	if err := db.QueryRow(`SELECT sum(up),sum(down) FROM client_traffics WHERE email='owned'`).Scan(&up, &down); err != nil || up != 40 || down != 60 {
		t.Fatal("failed fixture changed traffic", err)
	}
	if err := setTraffic(db, in); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT sum(up),sum(down) FROM client_traffics WHERE email='owned'`).Scan(&up, &down); err != nil || up != 222 || down != 444 {
		t.Fatal("traffic not updated", err)
	}
	if err := db.QueryRow(`SELECT up,down FROM client_traffics WHERE email='other'`).Scan(&up, &down); err != nil || up != 50 || down != 60 {
		t.Fatal("foreign client changed", err)
	}
}

func TestTrafficFixtureRejectsBadInput(t *testing.T) {
	good := `{"panel_key":"owned","vpn_id":"vpn","sub_id":"sub","up":111,"down":222,"memberships":2,"require_disabled":true}`
	if _, err := readInput(strings.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{good + `{}`, strings.Replace(good, `"up":111`, `"up":-1`, 1), strings.Replace(good, `"down":222`, `"down":1.5`, 1), strings.Replace(good, `"up":111`, `"unknown":111`, 1), `{}`} {
		if _, err := readInput(strings.NewReader(bad)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
