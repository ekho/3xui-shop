package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// LegacyServer retains every SQLite source field in its provenance snapshot.
type LegacyServer struct {
	SourceID        int64   `json:"source_id"`
	Name            string  `json:"name"`
	Host            string  `json:"host"`
	MaxClients      int64   `json:"max_clients"`
	Location        *string `json:"location"`
	Online          *bool   `json:"online"`
	SubscriptionURL *string `json:"subscription_url"`
}

func validateLegacyServer(v LegacyServer) (string, *string, error) {
	if v.SourceID <= 0 || v.MaxClients < 0 || v.Online == nil || !utf8.ValidString(v.Name) || strings.TrimSpace(v.Name) != v.Name || v.Name == "" || len([]rune(v.Name)) > 255 || strings.ContainsRune(v.Name, 0) {
		return "", nil, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	_, err := httpsURL(v.Host, false)
	if err != nil {
		return "", nil, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	var base *string
	if v.SubscriptionURL != nil {
		_, e := httpsURL(*v.SubscriptionURL, true)
		if e != nil {
			return "", nil, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		base = v.SubscriptionURL
	}
	if v.Location != nil && (!utf8.ValidString(*v.Location) || strings.ContainsRune(*v.Location, 0)) {
		return "", nil, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	return v.Host, base, nil
}

// ImportLegacyServersTx joins the caller transaction without seeding or probing panels.
func ImportLegacyServersTx(ctx context.Context, tx pgx.Tx, servers []LegacyServer) (int, error) {
	if tx == nil || servers == nil {
		return 0, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	count := 0
	for _, v := range servers {
		host, base, err := validateLegacyServer(v)
		if err != nil {
			return count, err
		}
		snapshot, err := json.Marshal(v)
		if err != nil {
			return count, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		id := strconv.FormatInt(v.SourceID, 10)
		var same bool
		err = tx.QueryRow(ctx, `SELECT server_id=$2 AND source_snapshot=$3::jsonb FROM legacy_server_imports WHERE source_id=$1 FOR UPDATE`, v.SourceID, id, snapshot).Scan(&same)
		if err == nil {
			if !same {
				return count, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return count, unavailable()
		}
		var occupied bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vpn_servers WHERE id=$1 OR name=$2 OR host=$3)`, id, v.Name, host).Scan(&occupied); err != nil {
			return count, unavailable()
		}
		if occupied {
			return count, failure(409, "SERVER_CONFLICT")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO vpn_servers(id,name,host,max_clients,subscription_base_url,online) VALUES($1,$2,$3,$4,$5,$6)`, id, v.Name, host, v.MaxClients, baseOrEmpty(base), *v.Online); err != nil {
			return count, failure(409, "SERVER_CONFLICT")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO legacy_server_imports(source_id,server_id,source_snapshot) VALUES($1,$2,$3::jsonb)`, v.SourceID, id, snapshot); err != nil {
			return count, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		count++
	}
	return count, nil
}

func baseOrEmpty(base *string) string {
	if base == nil {
		return ""
	}
	return *base
}
