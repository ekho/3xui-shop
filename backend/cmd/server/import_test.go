package main

import (
	"strings"
	"testing"
)

func TestLegacyApprovalCLIStrictPackage(t *testing.T) {
	good := `{"version":1,"users":[],"approval_events":[]}`
	if _, err := decodeLegacyApprovalPackage(strings.NewReader(good)); err != nil {
		t.Fatal("valid empty packet", err)
	}
	for _, body := range []string{
		`{"version":2,"users":[],"approval_events":[]}`,
		`{"version":1,"users":[],"approval_events":[],"payload":"secret"}`,
		`{"version":1,"users":[],"approval_events":[]} {}`,
	} {
		if _, err := decodeLegacyApprovalPackage(strings.NewReader(body)); err == nil {
			t.Fatal("invalid packet accepted")
		}
	}
}
