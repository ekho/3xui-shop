package platform

import (
	"net/url"
	"testing"
)

func TestYooMoneySignatureOfficialVector(t *testing.T) {
	fields := url.Values{
		"amount": {"98.00"}, "codepro": {"false"}, "currency": {"643"},
		"datetime": {"2013-12-26T08:28:34Z"}, "label": {"ML23045"},
		"notification_type": {"p2p-incoming"}, "operation_id": {"441361714955017004"},
		"sender": {"41000000000"}, "sha1_hash": {"ac13833bd6ba9eff1fa9e4bed76f3d6ebb57f6c0"},
		"unaccepted": {"false"}, "withdraw_amount": {"100.00"},
	}
	if got := yooMoneySignature(fields, []byte("secret123")); got != "a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605" {
		t.Fatalf("official signature vector: %s", got)
	}
	fields.Set("extra", "a b")
	if got := yooMoneySignature(fields, []byte("secret123")); got == "a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605" {
		t.Fatal("unknown signed field ignored")
	}
}
