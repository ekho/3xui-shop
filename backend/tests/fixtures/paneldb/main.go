// paneldb changes traffic only in the stopped, synthetic Docker panel fixture.
package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type trafficInput struct {
	PanelKey        string `json:"panel_key"`
	VPNID           string `json:"vpn_id"`
	SubID           string `json:"sub_id"`
	Up              int64  `json:"up"`
	Down            int64  `json:"down"`
	Memberships     int64  `json:"memberships"`
	RequireDisabled bool   `json:"require_disabled"`
}

func readInput(r io.Reader) (trafficInput, error) {
	var in trafficInput
	d := json.NewDecoder(io.LimitReader(r, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || in.PanelKey == "" || in.VPNID == "" || in.SubID == "" || in.Up < 0 || in.Down < 0 || in.Memberships < 0 {
		return in, errors.New("invalid fixture input")
	}
	return in, nil
}

func setTraffic(db *sql.DB, in trafficInput) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var clients int
	err = tx.QueryRow(`SELECT count(*) FROM clients WHERE email=? AND uuid=? AND sub_id=? AND (?=0 OR enable=0)`, in.PanelKey, in.VPNID, in.SubID, in.RequireDisabled).Scan(&clients)
	if err != nil || clients != 1 {
		return errors.New("fixture identity mismatch")
	}
	result, err := tx.Exec(`UPDATE client_traffics SET up=?,down=? WHERE email=?`, in.Up, in.Down, in.PanelKey)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed < 1 || (in.Memberships > 0 && changed != in.Memberships) {
		return errors.New("fixture membership mismatch")
	}
	return tx.Commit()
}

func run() error {
	in, err := readInput(os.Stdin)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", "file:/panel/x-ui.db?mode=rw")
	if err != nil {
		return err
	}
	defer db.Close()
	return setTraffic(db, in)
}

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "FIXTURE_FAILED")
		os.Exit(1)
	}
	fmt.Println("FIXTURE_OK")
}
